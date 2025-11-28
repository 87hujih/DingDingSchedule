package repository

import "gorm.io/gorm"

// Repository 仓库层集合，持有数据库连接
type Repository struct {
	db       *gorm.DB
	UserRepo UserRepository
}

// NewRepository 创建仓库实例
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{
		db:       db,
		UserRepo: NewUserRepository(db),
	}
}
