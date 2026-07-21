// Package model contains persistent core entities.
package model

import "time"

// User and role constants used across core services.
const (
	UserActive     = "active"
	UserDisabled   = "disabled"
	SuperAdminRole = "super_admin"
	MemberRole     = "member"
)

// User is a platform account.
type User struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string    `gorm:"size:32;uniqueIndex;not null" json:"username"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	Status       string    `gorm:"size:16;not null;index" json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Role provides metadata for a Casbin role.
type Role struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Description string    `gorm:"size:255;not null" json:"description"`
	Builtin     bool      `gorm:"not null;default:false" json:"builtin"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Config is a runtime configuration entry.
type Config struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Group     string    `gorm:"column:config_group;size:64;not null;uniqueIndex:uk_configs_group_key" json:"group"`
	Key       string    `gorm:"column:config_key;size:128;not null;uniqueIndex:uk_configs_group_key" json:"key"`
	Value     string    `gorm:"type:longtext;not null" json:"-"`
	Encrypted bool      `gorm:"not null;default:false" json:"encrypted"`
	Version   uint64    `gorm:"not null;default:1" json:"version"`
	UpdatedBy uint64    `gorm:"not null" json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
