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
	WorkerConcurrency int    `yaml:"worker_concurrency"`

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
		WorkerConcurrency: 1,
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
