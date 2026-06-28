//go:build windows

package telegram

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type TDJSON struct {
	dll            *windows.DLL
	createClientID *windows.Proc
	send           *windows.Proc
	receive        *windows.Proc
	execute        *windows.Proc
}

func LoadTDJSON() (*TDJSON, error) {
	loadedDLL, err := loadTDJSONFromKnownPaths()
	if err != nil {
		return nil, err
	}

	tdjson := &TDJSON{dll: loadedDLL}

	tdjson.createClientID, err = loadedDLL.FindProc("td_create_client_id")
	if err != nil {
		return nil, err
	}
	tdjson.send, err = loadedDLL.FindProc("td_send")
	if err != nil {
		return nil, err
	}
	tdjson.receive, err = loadedDLL.FindProc("td_receive")
	if err != nil {
		return nil, err
	}
	tdjson.execute, err = loadedDLL.FindProc("td_execute")
	if err != nil {
		return nil, err
	}

	return tdjson, nil
}

func loadTDJSONFromKnownPaths() (*windows.DLL, error) {
	directories := []string{}

	if cwd, err := os.Getwd(); err == nil {
		directories = append(directories, cwd, filepath.Join(cwd, "bin"))
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		directories = append(directories, exeDir, filepath.Join(exeDir, "bin"))
	}

	directories = append(directories, "")

	var lastErr error
	var lastPath string
	for _, dir := range directories {
		tdjsonPath := "tdjson.dll"
		if dir != "" {
			tdjsonPath = filepath.Join(dir, "tdjson.dll")
		}
		if dir != "" {
			if _, err := os.Stat(tdjsonPath); err != nil {
				continue
			}
		}

		if err := loadDependencyDLLs(dir); err != nil {
			lastErr = err
			lastPath = tdjsonPath
			continue
		}

		loadedDLL, err := loadDLLWithSearchPath(tdjsonPath)
		if err == nil {
			return loadedDLL, nil
		}
		lastErr = err
		lastPath = tdjsonPath
	}

	if lastErr == nil {
		return nil, errors.New("failed to load tdjson.dll from known locations")
	}
	return nil, fmt.Errorf("failed to load tdjson.dll from %s: %w", lastPath, lastErr)
}

func loadDependencyDLLs(dir string) error {
	deps := []string{"libcrypto-3-x64.dll", "libssl-3-x64.dll", "zlib1.dll"}
	for _, dep := range deps {
		path := dep
		if dir != "" {
			path = filepath.Join(dir, dep)
			if _, err := os.Stat(path); err != nil {
				return err
			}
		}
		if _, err := loadDLLWithSearchPath(path); err != nil {
			return err
		}
	}
	return nil
}

func loadDLLWithSearchPath(path string) (*windows.DLL, error) {
	if filepath.IsAbs(path) {
		handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
		if err != nil {
			return nil, err
		}
		return &windows.DLL{Name: path, Handle: handle}, nil
	}
	return windows.LoadDLL(path)
}

func (tdjson *TDJSON) Close() error {
	if tdjson == nil || tdjson.dll == nil {
		return nil
	}
	// Stop the receive loop, but do NOT Release() the DLL: TDLib's background
	// threads (and our receive goroutine) may still be calling into it, and
	// unloading it out from under them crashes during teardown. The library is
	// meant to live for the whole process; the OS reclaims it on exit.
	stopDispatcherFor(tdjson)
	return nil
}

func (tdjson *TDJSON) CreateClientID() int32 {
	result, _, _ := tdjson.createClientID.Call()
	return int32(result)
}

func (tdjson *TDJSON) Send(clientID int32, requestJSON string) error {
	requestPointer, err := windows.BytePtrFromString(requestJSON)
	if err != nil {
		return err
	}
	tdjson.send.Call(uintptr(clientID), uintptr(unsafe.Pointer(requestPointer)))
	return nil
}

func (tdjson *TDJSON) Receive(timeoutSeconds float64) (string, error) {
	timeoutBits := *(*uintptr)(unsafe.Pointer(&timeoutSeconds))

	responsePointer, _, _ := tdjson.receive.Call(timeoutBits)
	if responsePointer == 0 {
		return "", nil
	}

	return windows.BytePtrToString((*byte)(unsafe.Pointer(responsePointer))), nil
}

func (tdjson *TDJSON) Execute(requestJSON string) (string, error) {
	requestPointer, err := windows.BytePtrFromString(requestJSON)
	if err != nil {
		return "", err
	}

	responsePointer, _, _ := tdjson.execute.Call(uintptr(unsafe.Pointer(requestPointer)))
	if responsePointer == 0 {
		return "", errors.New("td_execute returned null")
	}

	return windows.BytePtrToString((*byte)(unsafe.Pointer(responsePointer))), nil
}
