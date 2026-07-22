package mysql

import (
	"context"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

type configRepository struct{ db *gorm.DB }

// NewConfigRepository creates a GORM-backed configuration repository.
func NewConfigRepository(db *gorm.DB) configcenter.Repository {
	return &configRepository{db: db}
}

func (r *configRepository) Create(ctx context.Context, c *model.Config) error {
	return query.Use(idempotency.DB(ctx, r.db)).Config.WithContext(ctx).Create(c)
}

func (r *configRepository) Get(ctx context.Context, id uint64) (*model.Config, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Config
	return q.WithContext(ctx).Where(q.ID.Eq(id)).First()
}

func (r *configRepository) List(ctx context.Context, group string, page, size int) ([]model.Config, int64, error) {
	return r.ListFiltered(ctx, group, "", "", "", page, size)
}

func (r *configRepository) GetPublic(ctx context.Context, group, key string) (*model.Config, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Config
	return q.WithContext(ctx).
		Where(q.Group.Eq(group), q.Key.Eq(key), q.Format.Eq("json"), q.Visibility.Eq("public"), q.Encrypted.Is(false)).
		First()
}

func (r *configRepository) ListFiltered(ctx context.Context, group, keyword, format, visibility string, page, size int) ([]model.Config, int64, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Config
	dao := q.WithContext(ctx)
	if group != "" {
		dao = dao.Where(q.Group.Eq(group))
	}
	if format != "" {
		dao = dao.Where(q.Format.Eq(format))
	}
	if visibility != "" {
		dao = dao.Where(q.Visibility.Eq(visibility))
	}
	if keyword != "" {
		dao = dao.Where(field.Or(q.Group.Like("%"+keyword+"%"), q.Key.Like("%"+keyword+"%")))
	}
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := dao.Order(q.ID.Asc()).Offset((page - 1) * size).Limit(size).Find()
	if err != nil {
		return nil, 0, err
	}
	return configValues(rows), total, nil
}

func (r *configRepository) UpdateVersion(ctx context.Context, c *model.Config, expected uint64) (bool, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Config
	updatedAt := time.Now()
	result, err := q.WithContext(ctx).
		Where(q.ID.Eq(c.ID), q.Version.Eq(expected)).
		UpdateSimple(
			q.Value.Value(c.Value),
			q.Visibility.Value(c.Visibility),
			q.Version.Value(c.Version),
			q.UpdatedBy.Value(c.UpdatedBy),
			q.UpdatedAt.Value(updatedAt),
		)
	c.UpdatedAt = updatedAt
	return result.RowsAffected == 1, err
}

func (r *configRepository) Delete(ctx context.Context, id uint64) error {
	q := query.Use(idempotency.DB(ctx, r.db)).Config
	_, err := q.WithContext(ctx).Where(q.ID.Eq(id)).Delete()
	return err
}

func (r *configRepository) DeleteVersion(ctx context.Context, id, expected uint64) (bool, error) {
	q := query.Use(idempotency.DB(ctx, r.db)).Config
	result, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(expected)).Delete()
	return result.RowsAffected == 1, err
}

func configValues(rows []*model.Config) []model.Config {
	values := make([]model.Config, len(rows))
	for i, row := range rows {
		values[i] = *row
	}
	return values
}
