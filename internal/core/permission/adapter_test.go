package permission

import (
	"testing"

	casbinmodel "github.com/casbin/casbin/v3/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testPolicyAdapter(t *testing.T) (*gormPolicyAdapter, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&policyRule{}); err != nil {
		t.Fatal(err)
	}
	return newGORMPolicyAdapter(db), db
}

func testPolicyModel(t *testing.T) casbinmodel.Model {
	t.Helper()
	model, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func TestPolicyAdapterLifecycle(t *testing.T) {
	adapter, db := testPolicyAdapter(t)
	if err := adapter.AddPolicy("p", "p", []string{"reader", "/api/v1/items/:id", "GET"}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.AddPolicies("g", "g", [][]string{{"user:1", "reader"}, {"user:2", "reader"}}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.AddPolicies("p", "p", nil); err != nil {
		t.Fatal(err)
	}

	loaded := testPolicyModel(t)
	if err := adapter.LoadPolicy(loaded); err != nil {
		t.Fatal(err)
	}
	if got := len(loaded["p"]["p"].Policy); got != 1 {
		t.Fatalf("loaded policy count = %d", got)
	}
	if got := len(loaded["g"]["g"].Policy); got != 2 {
		t.Fatalf("loaded grouping count = %d", got)
	}

	if err := adapter.RemovePolicy("p", "p", []string{"reader", "/api/v1/items/:id", "GET"}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.RemovePolicies("g", "g", [][]string{{"user:1", "reader"}}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.RemoveFilteredPolicy("g", "g", 0, "user:2"); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&policyRule{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("remaining rows = %d, err = %v", count, err)
	}
}

func TestPolicyAdapterSaveAndHelpers(t *testing.T) {
	adapter, db := testPolicyAdapter(t)
	source := testPolicyModel(t)
	source["p"]["p"].Policy = [][]string{{"admin", "/api/v1/configs", "POST"}}
	source["g"]["g"].Policy = [][]string{{"user:9", "admin"}}
	if err := adapter.SavePolicy(source); err != nil {
		t.Fatal(err)
	}

	var rows []policyRule
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("saved rows = %d", len(rows))
	}
	if got := ruleArray(newPolicyRule("p", []string{"a", "b", "c", "d", "e", "f", "ignored"})); len(got) != 7 {
		t.Fatalf("rule array = %#v", got)
	}

	empty := testPolicyModel(t)
	if err := adapter.SavePolicy(empty); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&policyRule{}).Count(new(int64)).Error; err != nil {
		t.Fatal(err)
	}
	if err := policyDelete(db, "p", -1, "x"); err == nil {
		t.Fatal("expected invalid negative field index")
	}
	if err := policyDelete(db, "p", 5, "x", "y"); err == nil {
		t.Fatal("expected overflowing field range")
	}
}
