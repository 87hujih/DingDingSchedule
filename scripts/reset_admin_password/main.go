package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// 用于重置 GoAdmin 管理员密码
// 使用方法: go run ./scripts/reset_admin_password -- <新密码>
// 然后执行生成的 SQL: mysql -h <host> -u <user> -p <database> < reset_password.sql
func main() {
	if len(os.Args) < 2 {
		fmt.Println("使用方法: go run ./scripts/reset_admin_password -- <新密码>")
		os.Exit(1)
	}

	newPassword := os.Args[1]
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		fmt.Println("生成密码哈希失败:", err)
		os.Exit(1)
	}

	sql := fmt.Sprintf("UPDATE goadmin_users SET password='%s' WHERE username='admin';\n", string(hash))

	if err := os.WriteFile("reset_password.sql", []byte(sql), 0644); err != nil {
		fmt.Println("写入 SQL 文件失败:", err)
		os.Exit(1)
	}

	fmt.Println("密码哈希已生成，SQL 文件: reset_password.sql")
	fmt.Println("执行以下命令更新密码:")
	fmt.Println("mysql -h 106.52.42.194 -u root -p123456 schedule_lezhi < reset_password.sql")
}
