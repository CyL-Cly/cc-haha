package types_test

import (
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func TestInitializedBotRecipientsThroughJIDAndBinaryWireConsumers(t *testing.T) {
	if len(types.BotJIDMap) != 206 || types.BotJIDMap[types.NewMetaAIJID] != types.MetaAIJID {
		t.Fatal("bot recipient inventory or Meta AI mapping changed")
	}
	for bot, recipient := range types.BotJIDMap {
		parsedBot, err := types.ParseJID(bot.String())
		if err != nil || parsedBot != bot || !bot.IsBot() || bot.Server != types.BotServer {
			t.Fatal("bot JID consumer", bot, err)
		}
		parsedRecipient, err := types.ParseJID(recipient.String())
		if err != nil || parsedRecipient != recipient || !recipient.IsBot() || recipient.Server != types.DefaultUserServer {
			t.Fatal("recipient JID consumer", recipient, err)
		}
		raw, err := waBinary.Marshal(waBinary.Node{Tag: "ack", Attrs: waBinary.Attrs{"to": recipient, "participant": bot}})
		if err != nil {
			t.Fatal(err)
		}
		node, err := waBinary.Unmarshal(raw[1:])
		if err != nil || node.Attrs["to"] != recipient || node.Attrs["participant"] != bot {
			t.Fatal("bot recipient binary wire round trip", bot, node, err)
		}
	}
	// Retain the public map's shared, eagerly initialized storage semantics.
	alias := types.BotJIDMap
	fixtureBot := types.NewJID("private-initializer-fixture", types.BotServer)
	if _, exists := alias[fixtureBot]; exists {
		t.Fatal("fixture collided with inventory")
	}
	alias[fixtureBot] = types.MetaAIJID
	t.Cleanup(func() { delete(alias, fixtureBot) })
	if types.BotJIDMap[fixtureBot] != types.MetaAIJID {
		t.Fatal("public map was rebuilt instead of shared")
	}
}
