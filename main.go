package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int validate_host_api(const cliproxy_host_api* host, uint32_t expected_abi) {
	if (host == NULL || host->abi_version != expected_abi || host->call == NULL || host->free_buffer == NULL) {
		return 0;
	}
	return 1;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
	"github.com/magicvr/cpa-grok-panel/internal/plugin"
)

const abiVersion uint32 = 1

var (
	runtimeMu sync.RWMutex
	runtime   = plugin.NewRuntime(nil)
)

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, pluginAPI *C.cliproxy_plugin_api) C.int {
	if pluginAPI == nil || C.validate_host_api(host, C.uint32_t(abiVersion)) == 0 {
		return 1
	}
	C.store_host_api(host)
	pluginAPI.abi_version = C.uint32_t(abiVersion)
	pluginAPI.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	pluginAPI.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	pluginAPI.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)

	runtimeMu.Lock()
	runtime = plugin.NewRuntime(cpaabi.NewHost(callHost))
	runtimeMu.Unlock()
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, cpaabi.Failure("invalid_method", "method is required", false))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	runtimeMu.RLock()
	current := runtime
	runtimeMu.RUnlock()
	raw := current.Call(C.GoString(method), requestBytes)
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	runtimeMu.Lock()
	_ = runtime.Shutdown()
	runtimeMu.Unlock()
}

func callHost(method string, payload []byte) ([]byte, error) {
	methodC := C.CString(method)
	defer C.free(unsafe.Pointer(methodC))
	var requestPtr *C.uint8_t
	var requestLen C.size_t
	if len(payload) > 0 {
		requestPtr = (*C.uint8_t)(C.CBytes(payload))
		requestLen = C.size_t(len(payload))
		defer C.free(unsafe.Pointer(requestPtr))
	}
	var response C.cliproxy_buffer
	if C.call_host_api(methodC, requestPtr, requestLen, &response) != 0 {
		return nil, errors.New("host call failed")
	}
	if response.ptr == nil || response.len == 0 {
		return []byte("null"), nil
	}
	out := C.GoBytes(unsafe.Pointer(response.ptr), C.int(response.len))
	C.free_host_buffer(response.ptr, response.len)
	return out, nil
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil {
		return
	}
	if len(raw) == 0 {
		response.ptr = nil
		response.len = 0
		return
	}
	ptr := C.CBytes(raw)
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

func main() {}
