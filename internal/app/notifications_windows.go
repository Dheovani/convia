package app

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

/*
nameNotifications makes Windows show "Convia" on a notification, where the Wails runtime registers the
executable's file name as the application's name.
*/
func nameNotifications() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return nameApplication(`Software\Classes\AppUserModelId\`+filepath.Base(executable), "Convia")
}

func nameApplication(path, name string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue("DisplayName", name)
}
