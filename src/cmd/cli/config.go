package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"AutoAnimeDownloader/src/internal/files"
	"AutoAnimeDownloader/src/internal/nyaa"

	"github.com/urfave/cli/v2"
)

func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Manage configuration",
		Subcommands: []*cli.Command{
			{
				Name:  "get",
				Usage: "Get current configuration",
				Action: func(c *cli.Context) error {
					return handleConfigGet()
				},
			},
			{
				Name:      "set",
				Usage:     "Set configuration value",
				ArgsUsage: "<key> <value>",
				Description: "Set any key of config.json by its JSON name — `config get` lists them all.\n" +
					"A dotted key reaches into a nested object (notifications.batch_window_seconds,\n" +
					"priorities.fansubs) and leaves its other fields untouched.\n" +
					"Value is parsed as JSON when it parses (3, true, [\"a\",\"b\"]); otherwise it is\n" +
					"taken as a string. A comma-separated value is split into a list when the key\n" +
					"already holds one.",
				Action: func(c *cli.Context) error {
					if c.NArg() != 2 {
						return fmt.Errorf("usage: config set <key> <value>  (run `config get` to see the keys)")
					}
					return handleConfigSet(c.Args().Get(0), c.Args().Get(1))
				},
			},
		},
	}
}

func prioritiesCommand() *cli.Command {
	return &cli.Command{
		Name:  "priorities",
		Usage: "Priority list helpers",
		Subcommands: []*cli.Command{
			{
				Name:  "defaults",
				Usage: "Show the default torrent priority lists",
				Action: func(c *cli.Context) error {
					return handlePriorityDefaults()
				},
			},
			{
				Name:  "get",
				Usage: "Show the saved torrent priority lists",
				Action: func(c *cli.Context) error {
					return handlePrioritiesGet()
				},
			},
			{
				Name:      "set",
				Usage:     "Save the torrent priority lists from a JSON file (- for stdin), and nothing else",
				ArgsUsage: "<file|->",
				Description: "Writes only `priorities` (PUT /config/priorities), as the Priorities page does: it doesn't run\n" +
					"the folder and torrent-client checks of a full `config set`. Start from `priorities get --json`.",
				Action: handlePrioritiesSet,
			},
		},
	}
}

func handleConfigGet() error {
	config, err := getClient().GetConfig()
	if err != nil {
		return mapClientError(err)
	}
	return printValue(config, "")
}

// handleConfigSet patches ONE key of config.json by its JSON name, without the CLI knowing the
// field list: it round-trips the config through a map, so every key the daemon serves is settable
// and a new field needs no change here. The old hand-written switch covered 7 of the 25 keys and
// answered "unknown config key" for the rest, which reads as "the field does not exist".
//
// The saved value is also the type oracle: a key that already holds a list accepts the
// comma-separated form, everything else is JSON-parsed and falls back to a plain string.
//
// A dotted key (`notifications.batch_window_seconds`) patches one field of a nested object. Without
// it, changing one field meant passing the whole object, and leaving a sibling out — the webhooks
// next to batch_window_seconds — erased it.
func handleConfigSet(key, value string) error {
	client := getClient()
	config, err := client.GetConfig()
	if err != nil {
		return mapClientError(err)
	}

	raw, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to encode current config: %w", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("failed to decode current config: %w", err)
	}

	parent, name, err := resolveConfigKey(fields, key)
	if err != nil {
		return err
	}
	parent[name] = parseConfigValue(parent[name], value)

	patched, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	var updated files.Config
	if err := json.Unmarshal(patched, &updated); err != nil {
		return fmt.Errorf("failed to decode config: %w", err)
	}

	if err := mapClientError(client.UpdateConfig(&updated)); err != nil {
		return err
	}

	if outputJSON {
		outputJSONResponse(map[string]string{"message": "Configuration updated"})
		return nil
	}
	fmt.Println(configSetSummary(key, value))
	return nil
}

// configSetSummary is the line `config set` prints. It echoes the value so a mistyped one shows up,
// except when the value holds webhooks: their URLs and headers carry tokens (the ntfy Bearer, the
// Discord URL), and echoing them put the secret in the terminal and its scrollback.
func configSetSummary(key, value string) string {
	norm := strings.ToLower(strings.ReplaceAll(key, "_", ""))
	if norm == "notifications" || norm == "notifications.webhooks" || strings.HasPrefix(norm, "notifications.webhooks.") {
		return fmt.Sprintf("Configuration updated: %s (value not shown: webhooks hold tokens)", key)
	}
	// The password and the API keys: GET /config never shows them, and neither does this.
	for _, secret := range files.SecretConfigKeys {
		if norm == strings.ReplaceAll(secret, "_", "") {
			return fmt.Sprintf("Configuration updated: %s (value not shown: it is a secret)", key)
		}
	}
	return fmt.Sprintf("Configuration updated: %s = %s", key, value)
}

func handlePriorityDefaults() error {
	priorities, err := getClient().GetPriorityDefaults()
	if err != nil {
		return mapClientError(err)
	}
	return printValue(priorities, "")
}

// resolveConfigKey walks a dotted key into fields and returns the object holding the last segment
// plus that segment's real JSON name, so the caller writes the value in place. Every segment is
// matched like a top-level key (matchConfigKey); a segment that isn't there, or a dot after a value
// that isn't an object, is an unknown key.
func resolveConfigKey(fields map[string]any, key string) (map[string]any, string, error) {
	segments := strings.Split(key, ".")
	parent := fields
	for i, segment := range segments {
		name, ok := matchConfigKey(parent, segment)
		if !ok {
			return nil, "", fmt.Errorf("unknown config key: %s (run `config get` to see the keys)", key)
		}
		if i == len(segments)-1 {
			return parent, name, nil
		}
		child, ok := parent[name].(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("unknown config key: %s (%s is not an object)", key, strings.Join(segments[:i+1], "."))
		}
		parent = child
	}
	return nil, "", fmt.Errorf("unknown config key: %s", key)
}

// matchConfigKey resolves a user-typed key to the real JSON name, ignoring case and underscores so
// `maxSearchPages`, `max_search_pages` and `MAX_SEARCH_PAGES` all land on the same field.
func matchConfigKey(fields map[string]any, key string) (string, bool) {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	want := norm(key)
	for name := range fields {
		if norm(name) == want {
			return name, true
		}
	}
	return "", false
}

// parseConfigValue turns the raw CLI string into the JSON value the field expects. current is the
// value already saved under that key and is used only to recognise a list.
func parseConfigValue(current any, value string) any {
	if _, isList := current.([]any); isList && !strings.HasPrefix(strings.TrimSpace(value), "[") {
		items := []any{}
		for _, item := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				items = append(items, trimmed)
			}
		}
		return items
	}

	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err == nil {
		return parsed
	}
	return value
}

func handlePrioritiesGet() error {
	config, err := getClient().GetConfig()
	if err != nil {
		return mapClientError(err)
	}
	return printValue(config.Priorities, "")
}

func handlePrioritiesSet(c *cli.Context) error {
	if err := requireArgs(c, 1, "priorities set <file|->"); err != nil {
		return err
	}
	var priorities nyaa.Priorities
	if err := readJSONArg(c.Args().Get(0), &priorities); err != nil {
		return err
	}
	if err := mapClientError(getClient().UpdatePriorities(priorities)); err != nil {
		return err
	}
	return printMessage("Priorities updated", "Priorities updated")
}

// readJSONArg decodes the JSON in a file, or in stdin for "-", into v.
func readJSONArg(path string, v any) error {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return nil
}
