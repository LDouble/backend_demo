package permission

import (
	"fmt"

	"github.com/casbin/casbin/v3/model"
	"github.com/casbin/casbin/v3/persist"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type policyRule struct {
	ID      uint64 `gorm:"primaryKey;autoIncrement"`
	Ptype   string `gorm:"size:100;index"`
	V0      string `gorm:"size:100;index"`
	V1      string `gorm:"size:100;index"`
	V2      string `gorm:"size:100;index"`
	V3      string `gorm:"size:100;index"`
	V4      string `gorm:"size:100;index"`
	V5      string `gorm:"size:100;index"`
	Managed bool   `gorm:"not null;default:false;index"`
}

func (policyRule) TableName() string { return "casbin_rule" }

type gormPolicyAdapter struct{ db *gorm.DB }

func newGORMPolicyAdapter(db *gorm.DB) *gormPolicyAdapter { return &gormPolicyAdapter{db: db} }

func (a *gormPolicyAdapter) LoadPolicy(target model.Model) error {
	rows := []policyRule{}
	if err := a.db.Order("id").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := persist.LoadPolicyArray(ruleArray(row), target); err != nil {
			return err
		}
	}
	return nil
}

func (a *gormPolicyAdapter) SavePolicy(source model.Model) error {
	rows := []policyRule{}
	for _, section := range []string{"p", "g"} {
		for ptype, assertion := range source[section] {
			for _, rule := range assertion.Policy {
				rows = append(rows, newPolicyRule(ptype, rule))
			}
		}
	}
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&policyRule{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
}

func (a *gormPolicyAdapter) AddPolicy(_ string, ptype string, rule []string) error {
	row := newPolicyRule(ptype, rule)
	return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func (a *gormPolicyAdapter) AddPolicies(_ string, ptype string, rules [][]string) error {
	rows := make([]policyRule, 0, len(rules))
	for _, rule := range rules {
		rows = append(rows, newPolicyRule(ptype, rule))
	}
	if len(rows) == 0 {
		return nil
	}
	return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

func (a *gormPolicyAdapter) RemovePolicy(_ string, ptype string, rule []string) error {
	return policyDelete(a.db, ptype, 0, rule...)
}

func (a *gormPolicyAdapter) RemovePolicies(_ string, ptype string, rules [][]string) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		for _, rule := range rules {
			if err := policyDelete(tx, ptype, 0, rule...); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *gormPolicyAdapter) RemoveFilteredPolicy(
	_ string,
	ptype string,
	fieldIndex int,
	fieldValues ...string,
) error {
	return policyDelete(a.db, ptype, fieldIndex, fieldValues...)
}

func newPolicyRule(ptype string, values []string) policyRule {
	row := policyRule{Ptype: ptype}
	destinations := []*string{&row.V0, &row.V1, &row.V2, &row.V3, &row.V4, &row.V5}
	for index, value := range values {
		if index >= len(destinations) {
			break
		}
		*destinations[index] = value
	}
	return row
}

func ruleArray(row policyRule) []string {
	result := []string{row.Ptype, row.V0, row.V1, row.V2, row.V3, row.V4, row.V5}
	for len(result) > 1 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return result
}

func policyDelete(db *gorm.DB, ptype string, fieldIndex int, values ...string) error {
	if fieldIndex < 0 || fieldIndex > 5 || fieldIndex+len(values) > 6 {
		return fmt.Errorf("invalid Casbin policy field range")
	}
	query := db.Where("ptype = ?", ptype)
	columns := []string{"v0", "v1", "v2", "v3", "v4", "v5"}
	for index, value := range values {
		if value != "" {
			query = query.Where(columns[fieldIndex+index]+" = ?", value)
		}
	}
	return query.Delete(&policyRule{}).Error
}

var _ persist.BatchAdapter = (*gormPolicyAdapter)(nil)
