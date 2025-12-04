package repository

import "gorm.io/gorm"

// Repository 仓库层集合
type Repository struct {
	UserRepo UserRepository
	DeptRepo DepartmentRepository
}

// NewRepository 创建仓库实例
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{
		UserRepo: NewUserRepository(db),
		DeptRepo: NewDepartmentRepository(db),
	}
}
