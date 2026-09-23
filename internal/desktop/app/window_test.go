package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"convia/internal/desktop/installations"
)

func TestWhatTheApplicationAsksOfItsWindow(t *testing.T) {
	var said [][2]string
	var named [][2]string

	made := New(slog.New(slog.NewTextHandler(io.Discard, nil)), installations.In(t.TempDir()), store(nil), nil)
	made.Start(context.Background(), Window{
		Notify: func(title, body string) { said = append(said, [2]string{title, body}) },
		Named:  func(open, quit string) { named = append(named, [2]string{open, quit}) },
	})

	made.Notify("Standup", "A new message")
	made.Named("Open Convia", "Quit")

	if len(said) != 1 || said[0] != [2]string{"Standup", "A new message"} {
		t.Errorf("the window was asked to say %v", said)
	}
	if len(named) != 1 || named[0] != [2]string{"Open Convia", "Quit"} {
		t.Errorf("the menu was named %v", named)
	}
}

func TestAnApplicationWithNoWindowDoesNotReachForOne(t *testing.T) {
	made := New(slog.New(slog.NewTextHandler(io.Discard, nil)), installations.In(t.TempDir()), store(nil), nil)
	made.Start(context.Background(), Window{})

	made.Notify("Standup", "A new message")
	made.Named("Open Convia", "Quit")
	made.tell(EventTopic, "something")
}
