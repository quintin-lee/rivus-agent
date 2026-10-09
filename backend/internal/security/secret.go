package security

import (
	"errors"
	"os"
	"strings"
)

// GetSecret 只从环境变量读取密钥，绝不从仓库/Prompt/事件中读取。
func GetSecret(envKey string) (string, error) {
	v := strings.TrimSpace(os.Getenv(envKey))
	if v == "" {
		return "", errors.New("secret " + envKey + " is not set")
	}
	return v, nil
}
