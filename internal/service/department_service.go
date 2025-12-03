package service

import (
	"context"
	"fmt"

	"schedule_server/global"
	"schedule_server/internal/model"
	"schedule_server/internal/repository"
)

// DepartmentService 部门服务
type DepartmentService struct {
	deptRepo repository.DepartmentRepository
}

// NewDepartmentService 创建部门服务
func NewDepartmentService(deptRepo repository.DepartmentRepository) *DepartmentService {
	return &DepartmentService{deptRepo: deptRepo}
}

// Sync 从钉钉同步部门数据到数据库
func (s *DepartmentService) Sync(ctx context.Context) error {
	// 从钉钉获取所有部门
	allDepts, err := global.DingTalk.FetchAllDepartments(ctx)
	if err != nil {
		return fmt.Errorf("获取钉钉部门失败: %w", err)
	}

	if len(allDepts) == 0 {
		global.Log.Warn("钉钉返回部门列表为空")
		return nil
	}

	// 构建 parentID 集合，用于判断叶子节点
	parentIDs := make(map[int64]struct{}, len(allDepts))
	for _, d := range allDepts {
		if d.ParentID > 0 {
			parentIDs[d.ParentID] = struct{}{}
		}
	}

	// 构建部门模型列表，标记叶子节点
	depts := make([]model.Department, 0, len(allDepts))
	deptIDs := make([]int64, 0, len(allDepts))
	leafCount := 0
	for _, d := range allDepts {
		_, hasChildren := parentIDs[d.DeptID]
		isLeaf := !hasChildren

		if isLeaf {
			leafCount++
		}

		depts = append(depts, model.Department{
			DeptID:   d.DeptID,
			Name:     d.Name,
			ParentID: d.ParentID,
			IsLeaf:   isLeaf,
			Status:   1,
		})
		deptIDs = append(deptIDs, d.DeptID)
	}

	// 调用 repository 执行数据库操作
	if err := s.deptRepo.SyncAll(ctx, depts, deptIDs); err != nil {
		return fmt.Errorf("同步部门到数据库失败: %w", err)
	}

	global.Log.Infof("部门数据同步完成，共 %d 条，其中叶子部门 %d 个", len(depts), leafCount)
	return nil
}

// GetLeafDepartments 获取所有叶子部门
func (s *DepartmentService) GetLeafDepartments(ctx context.Context) ([]model.Department, error) {
	return s.deptRepo.FindLeaf(ctx)
}
