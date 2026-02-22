package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/toolhub/toolhub/internal/mcp"
)

func main() {
	defs := mcp.ToolDefinitions()

	fmt.Fprintln(os.Stdout, "# MCP Tools (Generated)")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "This file is generated from `toolhub/internal/mcp/server.go`.")
	fmt.Fprintln(os.Stdout)

	for _, d := range defs {
		name, _ := d["name"].(string)
		desc, _ := d["description"].(string)
		fmt.Fprintf(os.Stdout, "- `%s`\n", name)
		if desc != "" {
			fmt.Fprintf(os.Stdout, "  - Description: %s\n", desc)
		}

		schema, _ := d["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		requiredRaw, _ := schema["required"].([]string)
		requiredSet := make(map[string]bool, len(requiredRaw))
		for _, r := range requiredRaw {
			requiredSet[r] = true
		}

		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		if len(keys) > 0 {
			fmt.Fprintln(os.Stdout, "  - Input:")
			for _, k := range keys {
				req := "optional"
				if requiredSet[k] {
					req = "required"
				}
				fmt.Fprintf(os.Stdout, "    - `%s` (%s)\n", k, req)
			}
		}
		fmt.Fprintln(os.Stdout)
	}
}
