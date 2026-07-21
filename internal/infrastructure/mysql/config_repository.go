package mysql

import (
	"context"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"gorm.io/gorm"
)

type configRepository struct{ query *query.Query }

// NewConfigRepository creates a GORM-backed configuration repository.
func NewConfigRepository(db *gorm.DB) configcenter.Repository {
	return &configRepository{query: query.Use(db)}
}

func (r *configRepository) Create(ctx context.Context, c *model.Config) error {
	return r.query.Config.WithContext(ctx).Create(c)
}

func (r *configRepository) Get(ctx context.Context, id uint64) (*model.Config, error) {
	return r.query.Config.WithContext(ctx).Where(r.query.Config.ID.Eq(id)).First()
}

func (r *configRepository) List(ctx context.Context, group string, page, size int) ([]model.Config, int64, error) {
	dao := r.query.Config.WithContext(ctx)
	if group != "" {
		dao = dao.Where(r.query.Config.Group.Eq(group))
	}
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := dao.Order(r.query.Config.ID.Asc()).Offset((page - 1) * size).Limit(size).Find()
	if err != nil {
		return nil, 0, err
	}
	return configValues(rows), total, nil
}

func (r *configRepository) UpdateVersion(ctx context.Context, c *model.Config, expected uint64) (bool, error) {
	result, err := r.query.Config.WithContext(ctx).
		Where(r.query.Config.ID.Eq(c.ID), r.query.Config.Version.Eq(expected)).
		UpdateSimple(
			r.query.Config.Value.Value(c.Value),
			r.query.Config.Version.Value(c.Version),
			r.query.Config.UpdatedBy.Value(c.UpdatedBy),
			r.query.Config.UpdatedAt.Value(time.Now()),
		)
	return result.RowsAffected == 1, err
}

func (r *configRepository) Delete(ctx context.Context, id uint64) error {
	_, err := r.query.Config.WithContext(ctx).Where(r.query.Config.ID.Eq(id)).Delete()
	return err
}

func configValues(rows []*model.Config) []model.Config {
	values := make([]model.Config, len(rows))
	for i, row := range rows {
		values[i] = *row
	}
	return values
}
