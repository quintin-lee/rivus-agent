package model

import (
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"rivus-agent-backend/internal/config"
)

func FromConfig(c config.Config) (model.ToolCallingChatModel, error) {
	return FromConfigWithLookup(c, nil)
}

func FromConfigWithLookup(c config.Config, lookup func(key string) (string, bool)) (model.ToolCallingChatModel, error) {
	cc := c
	if lookup != nil {
		if v, ok := lookup("model.provider"); ok && v != "" {
			cc.ModelProvider = v
		}
		if v, ok := lookup("model.base_url"); ok && v != "" {
			cc.ModelBaseURL = v
		}
		if v, ok := lookup("model.name"); ok && v != "" {
			cc.ModelName = v
		}
		if v, ok := lookup("model.api_key"); ok && v != "" {
			cc.ModelAPIKey = v
		}
	}
	switch cc.ModelProvider {
	case "openai_compat":
		return NewOpenAICompat(OpenAICompatConfig{
			BaseURL:   cc.ModelBaseURL,
			APIKey:    cc.ModelAPIKey,
			Model:     cc.ModelName,
			Timeout:   time.Duration(cc.ModelTimeoutS) * time.Second,
			MaxTokens: 4096,
		})
	default:
		return nil, fmt.Errorf("unsupported provider %q", cc.ModelProvider)
	}
}
