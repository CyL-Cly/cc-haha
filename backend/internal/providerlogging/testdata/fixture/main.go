package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerlogging"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func run() error {
	var in struct {
		Home     string
		Original struct {
			ID        string
			Arguments struct {
				Model, QuerySource, ThinkingType  string
				MessagesLength                    int
				Temperature                       float64
				Betas                             []string
				PermissionMode, PreviousRequestID json.RawMessage
				FastMode                          bool
			}
		}
	}
	if err := json.NewDecoder(bufio.NewReader(os.Stdin)).Decode(&in); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	if in.Home != cwd || in.Home != os.Getenv("HOME") || in.Home != os.Getenv("USERPROFILE") || !strings.HasPrefix(filepath.Base(in.Home), "cc-haha-query-log-source-") {
		return errors.New("private original log fixture required")
	}
	owner := cooperative.New()
	defer owner.Close()
	q, _ := providerlogging.New(owner)
	return owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		receipt, _ := ownerasync.NewOwnerFuture[struct{}](owner)
		events := []map[string]any{}
		a := in.Original.Arguments
		if err := q.LogQuery(scope, providerlogging.QueryInput{Model: a.Model, QuerySource: a.QuerySource, ThinkingType: a.ThinkingType, MessagesLength: a.MessagesLength, Temperature: a.Temperature, Betas: a.Betas, FastMode: a.FastMode, PreviousRequestID: a.PreviousRequestID, Environment: map[string]string{"ANTHROPIC_BASE_URL": os.Getenv("ANTHROPIC_BASE_URL")}}, a.PermissionMode); err != nil {
			return err
		}
		before := len(events)
		if err := q.AttachSink(scope, func(next *cooperative.Scope, event providerlogging.Event) error {
			keys := []json.RawMessage{}
			for _, property := range event.Metadata.OwnProperties() {
				keys = append(keys, property.NameJSON)
			}
			events = append(events, map[string]any{"eventName": event.Name, "metadata": event.Metadata, "metadataOwnKeys": keys, "async": event.Async})
			return receipt.Resolve(next, struct{}{})
		}); err != nil {
			return err
		}
		if _, err := receipt.Await(scope); err != nil {
			return err
		}
		b, err := json.Marshal(map[string]any{"originalID": in.Original.ID, "beforeAttach": before, "events": events, "blocked": []any{}, "secureReads": []any{}})
		if err == nil {
			fmt.Println(string(b))
		}
		return err
	})
}
