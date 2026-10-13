package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"regexp"
)

var nativeCommandName = regexp.MustCompile(`^[a-zA-Z0-9:_-]*$`)

func (m *MainPrint) nativeDisabledSlashInput(scope *cooperative.Scope, name, args string, uuid json.RawMessage) (*sessioncontroller.ProcessedInput, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	_, statErr := m.options.Native.stat("/" + name)
	if nativeCommandName.MatchString(name) && statErr != nil {
		text := "Unknown skill: " + name
		records := []protocol.JSONObject{m.factory.CreateLocalCaveat(), messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain(text)}))}
		if args != "" {
			records = append(records, m.factory.CreateSystemInformation(quoteMain("Args from unknown skill: "+args), "warning"))
		}
		return &sessioncontroller.ProcessedInput{Messages: records, NoQuery: true, NoQueryResultText: text, StartMillis: m.options.Native.NowMillis()}, nil
	}
	input := "/" + name
	if args != "" {
		input += " " + args
	}
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain(input), "uuid": uuid}))}}, nil
}
