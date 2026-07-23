package streaming

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"

	streamingabci "cosmossdk.io/store/streaming/abci"
)

const pluginEnvKeyPrefix = "COSMOS_SDK"

// HandshakeMap contains a map of each supported streaming's handshake config
var HandshakeMap = map[string]plugin.HandshakeConfig{
	"abci": streamingabci.Handshake,
}

// PluginMap contains a map of supported gRPC plugins
var PluginMap = map[string]plugin.Plugin{
	"abci": &streamingabci.ListenerGRPCPlugin{},
}

func GetPluginEnvKey(name string) string {
	return fmt.Sprintf("%s_%s", pluginEnvKeyPrefix, strings.ToUpper(name))
}

func GetPluginChecksumEnvKey(name string) string {
	return fmt.Sprintf("%s_%s_SHA256", pluginEnvKeyPrefix, strings.ToUpper(name))
}

func NewStreamingPlugin(name, logLevel string) (interface{}, error) {
	logger := hclog.New(&hclog.LoggerOptions{
		Output: hclog.DefaultOutput,
		Level:  toHclogLevel(logLevel),
		Name:   fmt.Sprintf("plugin.%s", name),
	})

	// We're a host. Start by launching the streaming process.
	env := os.Getenv(GetPluginEnvKey(name))
	cmdArgs, err := parsePluginCommand(env)
	if err != nil {
		return nil, err
	}
	if err := verifyPluginChecksum(name, cmdArgs[0]); err != nil {
		return nil, err
	}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: HandshakeMap[name],
		Managed:         true,
		Plugins:         PluginMap,
		//#nosec G204 -- Required to load plugins
		Cmd:    exec.Command(cmdArgs[0], cmdArgs[1:]...),
		Logger: logger,
		AllowedProtocols: []plugin.Protocol{
			plugin.ProtocolNetRPC, plugin.ProtocolGRPC,
		},
	})

	// Connect via RPC
	rpcClient, err := client.Client()
	if err != nil {
		return nil, err
	}

	// Request streaming plugin
	return rpcClient.Dispense(name)
}

func parsePluginCommand(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("streaming plugin command is empty")
	}

	args := make([]string, 0, 4)
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}

	for _, r := range command {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}

		switch {
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("streaming plugin command has unterminated quote")
	}
	flush()
	if len(args) == 0 {
		return nil, fmt.Errorf("streaming plugin command is empty")
	}
	if !filepath.IsAbs(args[0]) {
		return nil, fmt.Errorf("streaming plugin executable must be an absolute path: %s", args[0])
	}

	return args, nil
}

func verifyPluginChecksum(name, executable string) error {
	expected := strings.TrimSpace(os.Getenv(GetPluginChecksumEnvKey(name)))
	if expected == "" {
		return nil
	}
	expected = strings.TrimPrefix(strings.ToLower(expected), "sha256:")

	bz, err := os.ReadFile(executable)
	if err != nil {
		return fmt.Errorf("read streaming plugin executable for checksum: %w", err)
	}
	sum := sha256.Sum256(bz)
	actual := hex.EncodeToString(sum[:])
	if actual != expected {
		return fmt.Errorf("streaming plugin checksum mismatch for %s", executable)
	}

	return nil
}

func toHclogLevel(s string) hclog.Level {
	switch s {
	case "trace":
		return hclog.Trace
	case "debug":
		return hclog.Debug
	case "info":
		return hclog.Info
	case "warn":
		return hclog.Warn
	case "error":
		return hclog.Error
	default:
		return hclog.DefaultLevel
	}
}
