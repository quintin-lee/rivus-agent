package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是服务启动配置。密钥只走环境变量，不进仓库与 Prompt。
type Config struct {
	Addr              string `yaml:"addr"`
	DBPath            string `yaml:"db_path"`
	SkillsDir         string `yaml:"skills_dir"`
	WorkspaceDir      string `yaml:"workspace_dir"`
	KnowledgeDir      string `yaml:"knowledge_dir"`

	// S3 配置
	S3Endpoint        string   `yaml:"s3_endpoint"`           // S3 兼容端点 (如 MinIO, Ceph, AWS S3)
	S3Region          string   `yaml:"s3_region"`             // 区域
	S3Bucket          string   `yaml:"s3_bucket"`             // 存储桶名称
	S3AccessKeyID     string   `yaml:"s3_access_key_id"`      // 访问密钥 (通过 env 注入，yaml 标记 "-")
	S3SecretAccessKey   string  `yaml:"s3_secret_access_key" ` // 秘密密钥 (通过 env 注入)
	S3UseSSL          bool     `yaml:"s3_use_ssl"`            // 是否使用 SSL
	S3DisableForcePathStyle bool `yaml:"s3_force_path_style"` // 是否使用路径风格风格

	// S3 存储路径映射
	S3WorkspacePrefix   string   `yaml:"s3_workspace_prefix"` // 工作区前缀
	S3KnowledgePrefix   string   `yaml:"s3_knowledge_prefix"` // 知识库前缀
	WorkerConcurrency int    `yaml:"worker_concurrency"`

	// HTTPFetchAllowlist 限制 http_fetch 工具可访问的域名（精确匹配 host）；
	// 为空 = http_fetch 完全禁用（所有请求直接拒绝）。
	HTTPFetchAllowlist []string `yaml:"http_fetch_allowlist"`

	// Pprof 控制 /debug/pprof 是否注册：nil = 跟随认证（配置了 AuthToken 才开），
	// 显式 true/false 覆盖（env AGENT_PPROF，yaml pprof）。
	Pprof *bool `yaml:"pprof"`

	// RateLimitRPM 是 /api/v1/ 路由的每客户端（X-Owner-ID 或来源 IP）每分钟请求上限；
	// 0 = 关闭（默认，向后兼容）。
	RateLimitRPM int `yaml:"rate_limit_rpm"`

	// MCP 是 config.yaml 的 mcp 段；nil = 未配置（settings/API 权威）。
	// 非 nil = yaml 权威：启动时写穿 settings 并热应用。
	MCP *MCPConfig `yaml:"mcp,omitempty"`

	AuthToken string `yaml:"-"`

	ModelProvider string `yaml:"model_provider"`
	ModelName     string `yaml:"model_name"`
	ModelBaseURL  string `yaml:"model_base_url"`
	ModelAPIKey   string `yaml:"-"`
	ModelTimeoutS int    `yaml:"model_timeout_seconds"`

	MaxDurationS   int `yaml:"max_duration_seconds"`
	MaxModelCalls  int `yaml:"max_model_calls"`
	MaxToolCalls   int `yaml:"max_tool_calls"`
	MaxIterations  int `yaml:"max_iterations"`
	MaxOutputBytes int `yaml:"max_output_bytes"`

	LogLevel string `yaml:"log_level"`
}

func Default() Config {
	return Config{
		Addr:              ":8080",
		DBPath:            "./data/agent.db",
		SkillsDir:         "./skills",
		WorkspaceDir:      "./workspace",
		KnowledgeDir:      "./knowledge",
		WorkerConcurrency: 1,
		S3Endpoint:        "",
		S3Region:          "",
		S3Bucket:          "",
		S3UseSSL:          false,
		S3DisableForcePathStyle: false,
		S3WorkspacePrefix:  "",
		S3KnowledgePrefix:  "",
		ModelProvider:     "openai_compat",
		ModelName:         "gpt-4o-mini",
		ModelBaseURL:      "https://api.openai.com/v1",
		ModelTimeoutS:     120,
		MaxDurationS:      600,
		MaxModelCalls:     30,
		MaxToolCalls:      50,
		MaxIterations:     12,
		MaxOutputBytes:    200000,
		LogLevel:          "info",
	}
}

// Load 从 YAML（可选）+ 环境变量加载，环境变量优先。返回校验后的配置。
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return Config{}, fmt.Errorf("read config: %w", err)
			}
		} else if err := yaml.Unmarshal(b, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("AGENT_ADDR"); v != "" {
		c.Addr = v
	}
	if v := os.Getenv("AGENT_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("AGENT_SKILLS_DIR"); v != "" {
		c.SkillsDir = v
	}
	if v := os.Getenv("AGENT_WORKSPACE_DIR"); v != "" {
		c.WorkspaceDir = v
	}
	if v := os.Getenv("AGENT_KNOWLEDGE_DIR"); v != "" {
		c.KnowledgeDir = v
	}
	if v := os.Getenv("AGENT_S3_ENDPOINT"); v != "" {
		c.S3Endpoint = v
	}
	if v := os.Getenv("AGENT_S3_REGION"); v != "" {
		c.S3Region = v
	}
	if v := os.Getenv("AGENT_S3_BUCKET"); v != "" {
		c.S3Bucket = v
	}
	if v := os.Getenv("AGENT_S3_ACCESS_KEY_ID"); v != "" {
		c.S3AccessKeyID = v
	}
	if v := os.Getenv("AGENT_S3_SECRET_ACCESS_KEY"); v != "" {
		c.S3SecretAccessKey = v
	}
	if v := os.Getenv("AGENT_S3_USE_SSL"); v != "" {
		b, _ := strconv.ParseBool(v)
		c.S3UseSSL = b
	}
	if v := os.Getenv("AGENT_S3_DISABLE_FORCE_PATH_STYLE"); v != "" {
		b, _ := strconv.ParseBool(v)
		c.S3DisableForcePathStyle = b
	}
	if v := os.Getenv("AGENT_S3_WORKSPACE_PREFIX"); v != "" {
		c.S3WorkspacePrefix = v
	}
	if v := os.Getenv("AGENT_S3_KNOWLEDGE_PREFIX"); v != "" {
		c.S3KnowledgePrefix = v
	}
	if v := os.Getenv("AGENT_HTTP_FETCH_ALLOWLIST"); v != "" {
		var out []string
		for _, part := range strings.Split(v, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		c.HTTPFetchAllowlist = out
	}
	if v := os.Getenv("AGENT_RATE_LIMIT_RPM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.RateLimitRPM = n
		}
	}
	if v := os.Getenv("AGENT_PPROF"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Pprof = &b
		}
	}
	if v := os.Getenv("AGENT_AUTH_TOKEN"); v != "" {
		c.AuthToken = v
	}
	if v := os.Getenv("MODEL_PROVIDER"); v != "" {
		c.ModelProvider = v
	}
	if v := os.Getenv("MODEL_NAME"); v != "" {
		c.ModelName = v
	}
	if v := os.Getenv("MODEL_BASE_URL"); v != "" {
		c.ModelBaseURL = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("MODEL_API_KEY"); v != "" {
		c.ModelAPIKey = v
	}
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.WorkerConcurrency = n
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
}

func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db_path is required")
	}
	if c.WorkerConcurrency < 1 || c.WorkerConcurrency > 32 {
		return fmt.Errorf("worker_concurrency must be in [1,32]")
	}
	if c.ModelProvider != "openai_compat" {
		return fmt.Errorf("unsupported model_provider %q", c.ModelProvider)
	}
	if c.ModelName == "" || c.ModelBaseURL == "" {
		return fmt.Errorf("model_name and model_base_url are required")
	}
	if c.MaxIterations < 1 || c.MaxModelCalls < 1 || c.MaxToolCalls < 1 {
		return fmt.Errorf("budget limits must be >= 1")
	}
	return nil
}

// DefaultBudget 返回服务端预算上限（客户端申请值不得放宽此处）。
func (c Config) DefaultBudget() BudgetLimits {
	return BudgetLimits{
		MaxDurationSeconds: c.MaxDurationS,
		MaxModelCalls:      c.MaxModelCalls,
		MaxToolCalls:       c.MaxToolCalls,
		MaxIterations:      c.MaxIterations,
		MaxOutputBytes:     c.MaxOutputBytes,
	}
}

// BudgetLimits 与 domain.Budget 字段对齐，避免 config 依赖 domain。
type BudgetLimits struct {
	MaxDurationSeconds int `json:"max_duration_seconds"`
	MaxModelCalls      int `json:"max_model_calls"`
	MaxToolCalls       int `json:"max_tool_calls"`
	MaxIterations      int `json:"max_iterations"`
	MaxOutputBytes     int `json:"max_output_bytes"`
}

// MCPConfig is the config.yaml "mcp:" section.
type MCPConfig struct {
	Enabled bool            `yaml:"enabled"`
	Servers []MCPServerYaml `yaml:"servers"`
}

// MCPServerYaml mirrors mcp.ServerSpec; the config package does not import
// mcp (which imports store) to keep dependency direction one-way.
type MCPServerYaml struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}
