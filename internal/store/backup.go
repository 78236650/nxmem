package store

// 备份：VACUUM INTO 生成一致快照（WAL 下也安全），不碰原库。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BackupTo 把当前库快照写到 dst（目录不存在则创建）。
func (s *Store) BackupTo(dst string) error {
	if strings.TrimSpace(dst) == "" {
		return fmt.Errorf("备份路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	q := "'" + strings.ReplaceAll(dst, "'", "''") + "'"
	if _, err := s.db.Exec("VACUUM INTO " + q); err != nil {
		return fmt.Errorf("备份失败: %w", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return err
	}
	fmt.Printf("备份完成: %s (%d bytes)\n", dst, fi.Size())
	return nil
}
