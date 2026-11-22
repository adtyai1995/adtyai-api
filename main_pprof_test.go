/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPprofListenAddrIsLoopback(t *testing.T) {
	if got := pprofListenAddr(); got != "127.0.0.1:8005" {
		t.Fatalf("pprof listen address = %q", got)
	}
}

func TestRunnableEntrypointsDoNotUseFloatingUpstreamImages(t *testing.T) {
	files := []string{
		"README.md",
		"README.en.md",
		"README.zh_CN.md",
		"docs/installation/BT.md",
		"docker-compose.yml",
		"docker-compose.dev.yml",
		"deploy/adty/docker-compose.yml",
		"Dockerfile",
		"Dockerfile.dev",
	}
	forbidden := []string{
		"calciumion/new-api",
		":latest",
		"redis:7-alpine",
		"image: redis:7\n",
		"image: redis:7\r",
	}
	for _, name := range files {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Fatalf("%s still contains %q", name, needle)
			}
		}
		// The default stack pins China time. Other entrypoints must not copy that default.
		if name != "docker-compose.yml" && strings.Contains(text, "TZ=Asia/Shanghai") {
			t.Fatalf("%s still contains %q", name, "TZ=Asia/Shanghai")
		}
	}
	for _, name := range []string{"Dockerfile", "Dockerfile.dev"} {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "@sha256:") {
			t.Fatalf("%s base image has no digest", name)
		}
	}
	for _, name := range []string{".github/workflows/ci.yml", ".github/workflows/adty-ghcr.yml"} {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "govulncheck") {
			t.Fatalf("%s has no Go vulnerability scan step", name)
		}
	}
}

func TestRootComposeDoesNotPublishExampleSecrets(t *testing.T) {
	for _, name := range []string{"docker-compose.yml", "docker-compose.dev.yml"} {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if strings.Contains(text, "123456") {
			t.Fatalf("%s still contains the example password", name)
		}
		if strings.Contains(text, "\"3000:3000\"") || strings.Contains(text, "- \"3000:3000\"") {
			t.Fatalf("%s publishes port 3000 on every interface", name)
		}
	}
}

func TestRootComposeProjectSettings(t *testing.T) {
	body, err := os.ReadFile("docker-compose.yml")
	require.NoError(t, err)
	var compose struct {
		Name     string
		Services map[string]struct {
			ContainerName string `yaml:"container_name"`
			Command       any
			Environment   any
			Networks      []string
		}
		Networks map[string]struct {
			Name     string
			Driver   string
			External bool
		}
		Volumes map[string]struct{ Name string }
	}
	require.NoError(t, yaml.Unmarshal(body, &compose))
	require.Equal(t, "adtyai-api", compose.Name)
	require.Len(t, compose.Networks, 1)
	network := compose.Networks["new-api-network"]
	require.Equal(t, "bridge", network.Driver)
	require.Empty(t, network.Name)
	require.False(t, network.External)
	require.Contains(t, compose.Volumes, "pg_data")
	require.Empty(t, compose.Volumes["pg_data"].Name)

	for _, name := range []string{"new-api", "postgres", "redis"} {
		service := compose.Services[name]
		require.Empty(t, service.ContainerName, name)
		require.Equal(t, []string{"new-api-network"}, service.Networks, name)
		switch env := service.Environment.(type) {
		case map[string]any:
			require.Equal(t, "Asia/Shanghai", env["TZ"], name)
		case []any:
			require.Contains(t, env, "TZ=Asia/Shanghai", name)
		default:
			t.Fatalf("docker-compose.yml: missing environment for %s", name)
		}
	}
	postgres := compose.Services["postgres"]
	require.IsType(t, []any{}, postgres.Command)
	require.Contains(t, postgres.Command, "timezone=Asia/Shanghai")
	require.Contains(t, postgres.Command, "log_timezone=Asia/Shanghai")
}
