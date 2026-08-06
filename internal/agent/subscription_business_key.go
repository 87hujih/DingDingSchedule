package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const subscriptionBusinessKeyVersion = 1

type SubscriptionBusinessKeyV1 struct {
	Version        int      `json:"version"`
	TenantID       uint     `json:"tenant_id"`
	ConversationID string   `json:"conversation_id"`
	Operation      string   `json:"operation"`
	Scope          string   `json:"scope,omitempty"`
	DeptIDs        []uint64 `json:"dept_ids,omitempty"`
}

func subscriptionBusinessKeyForRequest(req OperationRequest) (string, error) {
	key := SubscriptionBusinessKeyV1{
		Version:        subscriptionBusinessKeyVersion,
		TenantID:       req.TenantID,
		ConversationID: strings.TrimSpace(req.ConversationID),
		Operation:      strings.TrimSpace(req.Operation),
	}
	if key.TenantID == 0 || key.ConversationID == "" {
		return "", errors.New("subscription business key identity is incomplete")
	}

	switch key.Operation {
	case "subscription.start":
		scope, ok := extractParamString(req.TrustedParams, "scope")
		if !ok {
			return "", errors.New("subscription business key scope is missing")
		}
		key.Scope = strings.TrimSpace(scope)
		deptIDs, _ := extractParamInt64Slice(req.TrustedParams, "dept_ids")
		normalized, err := normalizeBusinessKeyDeptIDs(deptIDs)
		if err != nil {
			return "", err
		}
		switch key.Scope {
		case "all":
			if len(normalized) != 0 {
				return "", errors.New("all scope cannot include department ids")
			}
		case "department":
			if len(normalized) == 0 {
				return "", errors.New("department scope requires department ids")
			}
			key.DeptIDs = normalized
		default:
			return "", fmt.Errorf("unsupported subscription scope %q", key.Scope)
		}
	case "subscription.cancel":
		// Cancellation identity is intentionally independent of actor and request.
	default:
		return "", fmt.Errorf("unsupported subscription write operation %q", key.Operation)
	}

	payload, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("marshal subscription business key: %w", err)
	}
	digest := sha256.Sum256(append([]byte("v1\n"), payload...))
	return hex.EncodeToString(digest[:]), nil
}

func normalizeBusinessKeyDeptIDs(ids []int64) ([]uint64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	unique := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("invalid department id %d", id)
		}
		unique[uint64(id)] = struct{}{}
	}
	result := make([]uint64, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}
