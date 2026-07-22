package mysql

import (
	"context"

	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"gorm.io/gorm"
)

type roleRepository struct{ db *gorm.DB }

// NewRoleRepository creates a GORM-backed role repository.
func NewRoleRepository(db *gorm.DB) permission.RoleRepository {
	return &roleRepository{db: db}
}

func (r *roleRepository) Create(ctx context.Context, v *model.Role) error {
	return query.Use(idempotency.DB(ctx, r.db)).Role.WithContext(ctx).Create(v)
}

func (r *roleRepository) Get(ctx context.Context, id uint64) (*model.Role, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Role
	return q.WithContext(ctx).Where(q.ID.Eq(id)).First()
}

func (r *roleRepository) GetByName(ctx context.Context, name string) (*model.Role, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Role
	return q.WithContext(ctx).Where(q.Name.Eq(name)).First()
}

func (r *roleRepository) List(ctx context.Context, page, size int) ([]model.Role, int64, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Role
	dao := q.WithContext(ctx)
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := dao.Order(q.ID.Asc()).Offset((page - 1) * size).Limit(size).Find()
	if err != nil {
		return nil, 0, err
	}
	return roleValues(rows), total, nil
}

func (r *roleRepository) UpdateDescription(ctx context.Context, id uint64, description string) error {
	q := query.Use(idempotency.DB(ctx, r.db)).Role
	_, err := q.WithContext(ctx).Where(q.ID.Eq(id)).UpdateSimple(q.Description.Value(description))
	return err
}

func (r *roleRepository) Delete(ctx context.Context, id uint64) error {
	q := query.Use(idempotency.DB(ctx, r.db)).Role
	_, err := q.WithContext(ctx).Where(q.ID.Eq(id)).Delete()
	return err
}

func roleValues(rows []*model.Role) []model.Role {
	values := make([]model.Role, len(rows))
	for i, row := range rows {
		values[i] = *row
	}
	return values
}
