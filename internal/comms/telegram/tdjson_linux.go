//go:build linux

package telegram

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

typedef int         (*fn_create_client_id)();
typedef void        (*fn_send)(int client_id, const char* request);
typedef const char* (*fn_receive)(double timeout);
typedef const char* (*fn_execute)(const char* request);

static void*               _handle           = NULL;
static fn_create_client_id _create_client_id = NULL;
static fn_send             _send             = NULL;
static fn_receive          _receive          = NULL;
static fn_execute          _execute          = NULL;

static int tdjson_load(const char* path) {
    void* h = dlopen(path, RTLD_NOW | RTLD_GLOBAL);
    if (!h) return 0;
    fn_create_client_id f1 = (fn_create_client_id)dlsym(h, "td_create_client_id");
    fn_send             f2 = (fn_send)dlsym(h, "td_send");
    fn_receive          f3 = (fn_receive)dlsym(h, "td_receive");
    fn_execute          f4 = (fn_execute)dlsym(h, "td_execute");
    if (!f1 || !f2 || !f3 || !f4) { dlclose(h); return 0; }
    _handle = h;
    _create_client_id = f1;
    _send = f2;
    _receive = f3;
    _execute = f4;
    return 1;
}

// Intentionally does NOT dlclose the handle. TDLib's tdjson runs background
// threads (network, receive) for the lifetime of the loaded library; calling
// dlclose() while they are still active runs the library's destructors out from
// under those threads and intermittently segfaults during process teardown.
// The library is meant to live for the whole process — the OS reclaims it on
// exit, so we just leave it loaded.
static void tdjson_close() {}

static int         tdjson_create_client_id()                  { return _create_client_id(); }
static void        tdjson_send(int id, const char* req)        { _send(id, req); }
static const char* tdjson_receive(double t)                    { return _receive(t); }
static const char* tdjson_execute(const char* req)             { return _execute(req); }
*/
import "C"

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"
)

type TDJSON struct{}

func LoadTDJSON() (*TDJSON, error) {
	for _, path := range soSearchPaths() {
		cpath := C.CString(path)
		ok := C.tdjson_load(cpath)
		C.free(unsafe.Pointer(cpath))
		if ok == 1 {
			return &TDJSON{}, nil
		}
	}
	return nil, errors.New("failed to load libtdjson.so from known locations; install TDLib or set TDLIB_BIN")
}

func soSearchPaths() []string {
	var paths []string

	addDir := func(dir string) {
		if dir == "" {
			return
		}
		paths = append(
			paths,
			filepath.Join(dir, "libtdjson.so"),
			filepath.Join(dir, "libtdjson.so.1"),
		)
	}

	if cwd, err := os.Getwd(); err == nil {
		addDir(cwd)
		addDir(filepath.Join(cwd, "bin"))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		addDir(exeDir)
		addDir(filepath.Join(exeDir, "bin"))
	}

	addDir("/usr/local/lib")
	addDir("/usr/lib")
	addDir("/usr/lib/x86_64-linux-gnu")
	addDir("/usr/lib/aarch64-linux-gnu")

	// Fall back to system linker search (LD_LIBRARY_PATH / ldconfig)
	paths = append(paths, "libtdjson.so")

	return paths
}

func (t *TDJSON) Close() error {
	stopDispatcherFor(t)
	C.tdjson_close()
	return nil
}

func (t *TDJSON) CreateClientID() int32 {
	return int32(C.tdjson_create_client_id())
}

func (t *TDJSON) Send(clientID int32, requestJSON string) error {
	cstr := C.CString(requestJSON)
	defer C.free(unsafe.Pointer(cstr))
	C.tdjson_send(C.int(clientID), cstr)
	return nil
}

func (t *TDJSON) Receive(timeoutSeconds float64) (string, error) {
	result := C.tdjson_receive(C.double(timeoutSeconds))
	if result == nil {
		return "", nil
	}
	return C.GoString(result), nil
}

func (t *TDJSON) Execute(requestJSON string) (string, error) {
	cstr := C.CString(requestJSON)
	defer C.free(unsafe.Pointer(cstr))
	result := C.tdjson_execute(cstr)
	if result == nil {
		return "", errors.New("td_execute returned null")
	}
	return C.GoString(result), nil
}
