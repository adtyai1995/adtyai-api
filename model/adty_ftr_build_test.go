package model

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestFTRADTY2307BuildAndDeployPinsPostgres18(t *testing.T) {
	for _, file := range []string{"../docker-compose.yml", "../deploy/adty/docker-compose.yml"} {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		var compose struct {
			Services map[string]struct {
				Image       string `yaml:"image"`
				Healthcheck struct {
					Test []string `yaml:"test"`
				} `yaml:"healthcheck"`
			} `yaml:"services"`
		}
		require.NoError(t, yaml.Unmarshal(data, &compose))
		require.Equal(t, "postgres:18", compose.Services["postgres"].Image, file)
		require.Equal(t, "redis:7.4.4-alpine", compose.Services["redis"].Image, file)
	}

	dev, err := os.ReadFile("../docker-compose.dev.yml")
	require.NoError(t, err)
	var devCompose struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(dev, &devCompose))
	require.Equal(t, "postgres:18-alpine", devCompose.Services["postgres"].Image)

	prod, err := os.ReadFile("../docker-compose.yml")
	require.NoError(t, err)
	require.Contains(t, string(prod), "/api/ready")

	mainSrc, err := os.ReadFile("../main.go")
	require.NoError(t, err)
	require.Contains(t, string(mainSrc), "//go:embed web/dist")

	rsbuild, err := os.ReadFile("../web/rsbuild.config.ts")
	require.NoError(t, err)
	require.Contains(t, string(rsbuild), "defineConfig")
	require.Contains(t, string(rsbuild), "@rsbuild/core")

	for _, file := range []string{"../.github/workflows/ci.yml", "../.github/workflows/adty-ghcr.yml"} {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Contains(t, string(data), "image: postgres:18")
		require.NotContains(t, string(data), "image: postgres:17")
	}
}
