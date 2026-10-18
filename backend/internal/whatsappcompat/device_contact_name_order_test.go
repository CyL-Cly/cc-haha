package whatsappcandidate

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func TestActualNodeContactSingleNameWriteUsesFirstThenFullAndRetainsRuntimeFields(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	device, _, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	person := types.NewJID("111", types.DefaultUserServer)
	address := types.NewADJID("111", 0, 7)
	if _, _, err := device.Contacts.PutPushName(ctx, person, "Push label"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := device.Contacts.PutBusinessName(ctx, person, "Business label"); err != nil {
		t.Fatal(err)
	}
	if err := device.Contacts.PutManyRedactedPhones(ctx, []store.RedactedPhoneEntry{{JID: person, RedactedPhone: "***111"}}); err != nil {
		t.Fatal(err)
	}
	// The real AppState dispatcher and the original SQL implementation both
	// call the public ContactStore setter with first name before full name.
	if err := device.Contacts.PutContactName(ctx, address, "Ada", "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	actual, err := device.Contacts.GetContact(ctx, person)
	want := types.ContactInfo{Found: true, FirstName: "Ada", FullName: "Ada Lovelace", PushName: "Push label", BusinessName: "Business label", RedactedPhone: "***111"}
	if err != nil || actual != want {
		t.Fatalf("public contact single name field order differs: got %+v, want %+v, err %v", actual, want, err)
	}
	byAddress, err := device.Contacts.GetContact(ctx, address)
	if err != nil || byAddress != want {
		t.Fatal("contact name write lost same-person device identity", byAddress, err)
	}
	all, err := device.Contacts.GetAllContacts(ctx)
	if err != nil || len(all) != 1 || all[person] != want {
		t.Fatal("public contact list differs after single name write", all, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := device.Contacts.PutContactName(canceled, person, "must-not-first", "must-not-full"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled contact name write did not reject", err)
	}
	retained, err := device.Contacts.GetContact(ctx, person)
	if err != nil || retained != want {
		t.Fatal("canceled contact name write mutated runtime fields", retained, err)
	}
	// Contacts stay ephemeral; this correction must not create auth files or
	// make runtime names survive a cold device instance.
	cold, _, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	coldValue, err := cold.Contacts.GetContact(ctx, person)
	if err != nil || coldValue.Found {
		t.Fatal("contact names leaked into cold auth state", coldValue, err)
	}
}

func TestActualNodeContactBulkNamesAndIncrementalUpdateKeepFieldOrder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	device, _, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	first := types.NewJID("111", types.DefaultUserServer)
	second := types.NewJID("222", types.DefaultUserServer)
	if err := device.Contacts.PutAllContactNames(ctx, []store.ContactEntry{
		{JID: types.NewADJID("111", 0, 3), FirstName: "原始名", FullName: "原始完整姓名"},
		{JID: second, FirstName: "Grace", FullName: "Grace Hopper"},
	}); err != nil {
		t.Fatal(err)
	}
	all, err := device.Contacts.GetAllContacts(ctx)
	wantFirst := types.ContactInfo{Found: true, FirstName: "原始名", FullName: "原始完整姓名"}
	wantSecond := types.ContactInfo{Found: true, FirstName: "Grace", FullName: "Grace Hopper"}
	if err != nil || len(all) != 2 || all[first] != wantFirst || all[second] != wantSecond {
		t.Fatal("bulk contact first/full fields differ", all, err)
	}
	if err := device.Contacts.PutContactName(ctx, first, "更新名", "更新完整姓名"); err != nil {
		t.Fatal(err)
	}
	wantFirst.FirstName = "更新名"
	wantFirst.FullName = "更新完整姓名"
	all, err = device.Contacts.GetAllContacts(ctx)
	if err != nil || len(all) != 2 || all[first] != wantFirst || all[second] != wantSecond {
		t.Fatal("bulk and incremental contact name field order diverged", all, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := device.Contacts.PutAllContactNames(canceled, []store.ContactEntry{{JID: second, FirstName: "bad-first", FullName: "bad-full"}}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled bulk contact write did not reject", err)
	}
	all, err = device.Contacts.GetAllContacts(ctx)
	if err != nil || len(all) != 2 || all[first] != wantFirst || all[second] != wantSecond {
		t.Fatal("canceled bulk contact write mutated runtime fields", all, err)
	}
	cold, _, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	all, err = cold.Contacts.GetAllContacts(ctx)
	if err != nil || len(all) != 0 {
		t.Fatal("bulk contact names leaked across cold auth state", all, err)
	}
}
