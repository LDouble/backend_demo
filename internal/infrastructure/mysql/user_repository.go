package mysql

import (
	"context"

	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"gorm.io/gorm"
)

// UserRepository is a GORM user repository.
type userRepository struct{ query *query.Query }

// NewUserRepository creates a GORM-backed user repository.
func NewUserRepository(db *gorm.DB) user.Repository {
	return &userRepository{query: query.Use(db)}
}

func (r *userRepository) Create(ctx context.Context, u *model.User) error {
	return r.query.User.WithContext(ctx).Create(u)
}

func (r *userRepository) GetByID(ctx context.Context, id uint64) (*model.User, error) {
	return r.query.User.WithContext(ctx).Where(r.query.User.ID.Eq(id)).First()
}

func (r *userRepository) GetByUsername(ctx context.Context, name string) (*model.User, error) {
	return r.query.User.WithContext(ctx).Where(r.query.User.Username.Eq(name)).First()
}

func (r *userRepository) List(ctx context.Context, page, size int) ([]model.User, int64, error) {
	dao := r.query.User.WithContext(ctx)
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := dao.Order(r.query.User.ID.Asc()).Offset((page - 1) * size).Limit(size).Find()
	if err != nil {
		return nil, 0, err
	}
	return userValues(rows), total, nil
}

func (r *userRepository) Update(ctx context.Context, u *model.User) error {
	return r.query.User.WithContext(ctx).Save(u)
}

func userValues(rows []*model.User) []model.User {
	values := make([]model.User, len(rows))
	for i, row := range rows {
		values[i] = *row
	}
	return values
}
