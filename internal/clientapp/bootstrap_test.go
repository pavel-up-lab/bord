package clientapp

import (
	"context"
	"path/filepath"
	"testing"

	"bord/internal/client"
	"bord/internal/tui"
)

func TestSavePersistsSelectedImageMode(t *testing.T) {
	state, err := client.NewState("https://bord.example")
	if err != nil {
		t.Fatal(err)
	}
	store := client.NewStore(filepath.Join(t.TempDir(), "state.json"))
	runtime := &Runtime{State: state, Store: store}
	model := tui.NewModel(context.Background(), nil).WithAttachmentViewer(tui.InlineANSI, nil)
	if err := runtime.Save(model); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.InlineMode != "ansi" {
		t.Fatalf("inline mode=%q", loaded.InlineMode)
	}
}
