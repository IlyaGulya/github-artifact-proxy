package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/v60/github"
	"gopkg.in/yaml.v3"
)

const (
	envTokenPrefix = "GITHUB_TOKEN_"
)

type Run struct {
	ID        int64
	Artifacts []*github.Artifact
	FetchTime time.Time
}

type LatestFilter struct {
	Branch *string `yaml:"branch"`
	Event  *string `yaml:"event"`
	Status *string `yaml:"status"`
}

type Target struct {
	Token        *string       `yaml:"token"`
	Owner        string        `yaml:"owner"`
	Repo         string        `yaml:"repo"`
	Filename     string        `yaml:"filename"`
	LatestFilter *LatestFilter `yaml:"latest_filter"`

	lockChan chan struct{}
	runCache map[string]*Run
}

type Webhook struct {
	Path   string `yaml:"path"`
	Secret string `yaml:"secret"`
}

type Config struct {
	Webhook *Webhook
	Tokens  map[string]string  `yaml:"tokens"`
	Targets map[string]*Target `yaml:"targets"`
}

func (t *Target) Lock(ctx context.Context) error {
	select {
	case t.lockChan <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Target) Unlock() {
	<-t.lockChan
}

// loadTokensFromEnv loads tokens from environment variables in the format GITHUB_TOKEN_[ID]
func loadTokensFromEnv() map[string]string {
	tokens := make(map[string]string)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, envTokenPrefix) {
			continue
		}
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}

		id := strings.ToLower(strings.TrimPrefix(parts[0], envTokenPrefix))
		tokens[id] = parts[1]
	}
	return tokens
}

func LoadConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config Config
	if err := yaml.NewDecoder(file).Decode(&config); err != nil {
		return nil, err
	}

	// Initialize tokens map if nil
	if config.Tokens == nil {
		config.Tokens = make(map[string]string)
	}

	// Load and merge tokens from environment variables
	envTokens := loadTokensFromEnv()
	for id, token := range envTokens {
		config.Tokens[id] = token
	}

	for id, target := range config.Targets {
		target.lockChan = make(chan struct{}, 1)
		target.runCache = make(map[string]*Run)

		if target.Token == nil {
			return nil, fmt.Errorf("target '%s' requires an API token", id)
		}

		if _, ok := config.Tokens[*target.Token]; !ok {
			return nil, fmt.Errorf("token with id '%s' not found in tokens list or environment variables", *target.Token)
		}
	}

	return &config, err
}
