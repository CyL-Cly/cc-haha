package main

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func commandTree(command *cobra.Command) map[string]any {
	flags := []map[string]any{}
	command.Flags().VisitAll(func(flag *pflag.Flag) {
		flags = append(flags, map[string]any{"name": flag.Name, "short": flag.Shorthand, "hidden": flag.Hidden, "description": flag.Usage})
	})
	children := []map[string]any{}
	for _, child := range command.Commands() {
		children = append(children, commandTree(child))
	}
	sort.Slice(children, func(i, j int) bool { return children[i]["name"].(string) < children[j]["name"].(string) })
	aliases := append([]string{}, command.Aliases...)
	return map[string]any{"name": command.Name(), "aliases": aliases, "description": command.Short, "hidden": command.Hidden, "hasAction": command.RunE != nil, "use": command.Use, "version": command.Version, "options": flags, "commands": children}
}

func main() {
	var input struct {
		Operation string   `json:"operation"`
		Args      []string `json:"args"`
		XAA       bool     `json:"xaa"`
		Version   string   `json:"version"`
		Profile   string   `json:"profile"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	if input.Operation == "spec" {
		os.Stdout.Write(cliargs.Specification())
		return
	}
	if input.Operation == "tree" {
		command, err := cliargs.NewCommand(input.Profile)
		if err != nil {
			panic(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(commandTree(command)); err != nil {
			panic(err)
		}
		return
	}
	if err := json.NewEncoder(os.Stdout).Encode(cliargs.Parse(input.Args, cliargs.Config{XAA: input.XAA, Version: input.Version})); err != nil {
		panic(err)
	}
}
