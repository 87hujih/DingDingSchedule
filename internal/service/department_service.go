package service

import (
	"context"
	"errors"
	"fmt"

	"schedule_server/internal/model"
	"schedule_server/internal/repository"
	"schedule_server/internal/response"

	"gorm.io/gorm"
)

// DepartmentService 部门服务
type DepartmentService struct {
	deptRepo repository.DepartmentRepository
	dingMgr  *DingTalkClientManager
}

// NewDepartmentService 创建部门服务
func NewDepartmentService(deptRepo repository.DepartmentRepository, dingMgr *DingTalkClientManager) *DepartmentService {
	return &DepartmentService{deptRepo: deptRepo, dingMgr: dingMgr}
}

// Sync 从钉钉同步部门数据到数据库
func (s *DepartmentService) Sync(ctx context.Context) error {
	if s.dingMgr == nil {
		return response.NewBizError(response.CodeInternalError, "钉钉租户管理器未初始化")
	}
	_, dingClient, err := s.dingMgr.FromContext(ctx)
	if err != nil {
		return response.NewBizError(response.CodeUnauthorized, "缺少租户信息")
	}

	// 从钉钉获取所有部门
	allDepts, err := dingClient.FetchAllDepartments(ctx)
	if err != nil {
		return fmt.Errorf("获取钉钉部门失败: %w", err)
	}

	if len(allDepts) == 0 {
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

	return nil
}

// List 获取所有叶子部门（需要考勤的部门）
func (s *DepartmentService) List(ctx context.Context) ([]model.Department, error) {
	return s.deptRepo.FindLeaf(ctx)
}

// UpdateStatus 更新部门考勤状态
func (s *DepartmentService) UpdateStatus(ctx context.Context, deptID int64, status int) error {
	if deptID <= 0 {
		return response.ErrInvalidParam()
	}

	if status != 0 && status != 1 {
		return response.ErrInvalidParamWithMsg("状态值无效")
	}

	if err := s.deptRepo.UpdateStatus(ctx, deptID, status); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("部门不存在")
		}
		return fmt.Errorf("更新部门状态失败: %w", err)
	}

	return nil
}

// Delete 删除部门（软删除）
func (s *DepartmentService) Delete(ctx context.Context, deptID int64) error {
	if deptID <= 0 {
		return response.ErrInvalidParam()
	}

	if err := s.deptRepo.Delete(ctx, deptID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.ErrNotFoundWithMsg("部门不存在")
		}
		return fmt.Errorf("删除部门失败: %w", err)
	}

	return nil
}
