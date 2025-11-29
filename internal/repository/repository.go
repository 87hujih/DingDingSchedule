package repository

import "gorm.io/gorm"

// Repository 仓库层集合
type Repository struct {
	UserRepo UserRepository
}

// NewRepository 创建仓库实例
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{
		UserRepo: NewUserRepository(db),
	}
}
