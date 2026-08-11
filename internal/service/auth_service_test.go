package service

import (
	"path/filepath"
	"testing"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/repository"
)

func newTestRepo(t *testing.T) *repository.PermissionRepo {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg := &config.Config{DBDialect: "sqlite", DBDSN: dbPath}
	db, err := repository.OpenDB(cfg)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := repository.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 测试结束后关闭连接，释放文件锁（Windows 下 TempDir 清理需要）
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return repository.NewPermissionRepo(db)
}

func TestAuthFlow(t *testing.T) {
	repo := newTestRepo(t)
	auth := NewAuthService(repo, "test-secret")

	// 首次安装生成账号
	user, pw, created, err := auth.EnsureBootstrapUser()
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !created || user != "admin" || pw == "" {
		t.Fatalf("bootstrap unexpected: %q %q %v", user, pw, created)
	}
	// 再次调用不应重复创建
	if _, _, created2, err := auth.EnsureBootstrapUser(); err != nil || created2 {
		t.Fatalf("second bootstrap should be no-op, got created=%v err=%v", created2, err)
	}

	// 登录成功
	u, err := auth.Authenticate("admin", pw)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.PasswordHash != "" || u.Salt != "" {
		t.Fatalf("password hash leaked in response")
	}

	// 错误密码
	if _, err := auth.Authenticate("admin", "wrong"); err == nil {
		t.Fatalf("expected auth failure for wrong password")
	}

	// 会话签发与校验
	token, err := auth.IssueSession(u.Username, u.DisplayName)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	if uname, _, ok := auth.VerifySession(token); !ok || uname != "admin" {
		t.Fatalf("verify session failed: %q %v", uname, ok)
	}

	// 首次必须改密、且无需旧密码
	if err := auth.ChangePassword("admin", "", "newpass123"); err != nil {
		t.Fatalf("first change password: %v", err)
	}
	// 改密后再次改密需旧密码
	if err := auth.ChangePassword("admin", "", "another123"); err == nil {
		t.Fatalf("expected old password required after first change")
	}
	if err := auth.ChangePassword("admin", "newpass123", "another123"); err != nil {
		t.Fatalf("change with old password: %v", err)
	}
	// 短密码应被拒绝
	if err := auth.ChangePassword("admin", "another123", "123"); err == nil {
		t.Fatalf("expected short password rejected")
	}
}
