package jwt

import (
	"testing"
	"time"
)

func TestManager_GenerateAndParse(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Secret: "test-secret-key-for-unit-test",
		Expire: time.Hour,
		Issuer: "test-issuer",
	}
	mgr := NewManager(cfg)

	cases := []struct {
		name       string
		userID     uint
		dingUserID string
		userName   string
	}{
		{
			name:       "normal user",
			userID:     1,
			dingUserID: "ding123456",
			userName:   "张三",
		},
		{
			name:       "user with special chars",
			userID:     999,
			dingUserID: "ding-abc_123",
			userName:   "李四",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// 签发Token
			token, err := mgr.GenerateToken(tc.userID, tc.dingUserID, tc.userName)
			if err != nil {
				t.Fatalf("GenerateToken failed: %v", err)
			}
			if token == "" {
				t.Fatal("GenerateToken returned empty token")
			}

			// 解析Token
			claims, err := mgr.ParseToken(token)
			if err != nil {
				t.Fatalf("ParseToken failed: %v", err)
			}

			// 验证载荷
			if claims.UserID != tc.userID {
				t.Errorf("UserID mismatch: got %d, want %d", claims.UserID, tc.userID)
			}
			if claims.DingUserID != tc.dingUserID {
				t.Errorf("DingUserID mismatch: got %s, want %s", claims.DingUserID, tc.dingUserID)
			}
			if claims.Name != tc.userName {
				t.Errorf("Name mismatch: got %s, want %s", claims.Name, tc.userName)
			}
			if claims.Issuer != cfg.Issuer {
				t.Errorf("Issuer mismatch: got %s, want %s", claims.Issuer, cfg.Issuer)
			}
		})
	}
}

func TestManager_ParseToken_Errors(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Secret: "test-secret",
		Expire: time.Hour,
		Issuer: "test",
	}
	mgr := NewManager(cfg)

	cases := []struct {
		name      string
		token     string
		wantError error
	}{
		{
			name:      "malformed token",
			token:     "not-a-valid-jwt",
			wantError: ErrTokenMalformed,
		},
		{
			name:      "empty token",
			token:     "",
			wantError: ErrTokenMalformed,
		},
		{
			name:      "invalid signature",
			token:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoxfQ.wrong_signature",
			wantError: ErrTokenInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := mgr.ParseToken(tc.token)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err != tc.wantError {
				t.Errorf("error mismatch: got %v, want %v", err, tc.wantError)
			}
		})
	}
}

func TestManager_ExpiredToken(t *testing.T) {
	t.Parallel()

	// 创建一个已过期的配置
	cfg := Config{
		Secret: "test-secret",
		Expire: -time.Hour, // 负数，立即过期
		Issuer: "test",
	}
	mgr := NewManager(cfg)

	token, err := mgr.GenerateToken(1, "ding123", "test")
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	_, err = mgr.ParseToken(token)
	if err != ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}
