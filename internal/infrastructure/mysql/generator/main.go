// Command generator creates type-safe GORM queries from core persistence models.
package main

import (
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"gorm.io/gen"
)

func main() {
	generator := gen.NewGenerator(gen.Config{
		OutPath: "./query",
		Mode:    gen.WithDefaultQuery | gen.WithQueryInterface,
	})
	generator.ApplyBasic(model.User{}, model.Role{}, model.Config{})
	applyGeneratedModules(generator)
	generator.Execute()
}
