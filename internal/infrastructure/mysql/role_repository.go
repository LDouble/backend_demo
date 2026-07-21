package mysql

import (
	"context"

	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"gorm.io/gorm"
)

type roleRepository struct{ query *query.Query }

// NewRoleRepository creates a GORM-backed role repository.
func NewRoleRepository(db *gorm.DB) permission.RoleRepository {
	return &roleRepository{query: query.Use(db)}
}

func (r *roleRepository) Create(ctx context.Context, v *model.Role) error {
	return r.query.Role.WithContext(ctx).Create(v)
}

func (r *roleRepository) Get(ctx context.Context, id uint64) (*model.Role, error) {
	return r.query.Role.WithContext(ctx).Where(r.query.Role.ID.Eq(id)).First()
}

func (r *roleRepository) GetByName(ctx context.Context, name string) (*model.Role, error) {
	return r.query.Role.WithContext(ctx).Where(r.query.Role.Name.Eq(name)).First()
}

func (r *roleRepository) List(ctx context.Context, page, size int) ([]model.Role, int64, error) {
	dao := r.query.Role.WithContext(ctx)
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := dao.Order(r.query.Role.ID.Asc()).Offset((page - 1) * size).Limit(size).Find()
	if err != nil {
		return nil, 0, err
	}
	return roleValues(rows), total, nil
}

func (r *roleRepository) Update(ctx context.Context, v *model.Role) error {
	return r.query.Role.WithContext(ctx).Save(v)
}

func (r *roleRepository) Delete(ctx context.Context, id uint64) error {
	_, err := r.query.Role.WithContext(ctx).Where(r.query.Role.ID.Eq(id)).Delete()
	return err
}

func roleValues(rows []*model.Role) []model.Role {
	values := make([]model.Role, len(rows))
	for i, row := range rows {
		values[i] = *row
	}
	return values
}
