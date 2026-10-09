package model

import (
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"rivus-agent-backend/internal/config"
)

func FromConfig(c config.Config) (model.ToolCallingChatModel, error) {
	switch c.ModelProvider {
	case "openai_compat":
		return NewOpenAICompat(OpenAICompatConfig{
			BaseURL:   c.ModelBaseURL,
			APIKey:    c.ModelAPIKey,
			Model:     c.ModelName,
			Timeout:   time.Duration(c.ModelTimeoutS) * time.Second,
			MaxTokens: 4096,
		})
	default:
		return nil, fmt.Errorf("unsupported provider %q", c.ModelProvider)
	}
}
