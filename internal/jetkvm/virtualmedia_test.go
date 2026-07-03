package jetkvm

import (
	"context"
	"testing"
	"time"
)

func TestVirtualMediaRPCCalls(t *testing.T) {
	_, client := deviceRPCServer(t, func(method string) any {
		switch method {
		case "getVirtualMediaState":
			return VirtualMediaState{Source: SourceStorage, Mode: ModeCDROM, Filename: "ubuntu.iso", Size: 42}
		case "listStorageFiles":
			return StorageFiles{Files: []StorageFile{{Filename: "ubuntu.iso", Size: 42}}}
		case "getStorageSpace":
			return StorageSpace{BytesUsed: 1, BytesFree: 2}
		case "checkMountUrl":
			return VirtualMediaURLInfo{Usable: true, Size: 99}
		default:
			return nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("GetVirtualMediaState", func(t *testing.T) {
		state, err := GetVirtualMediaState(ctx, client)
		if err != nil {
			t.Fatalf("GetVirtualMediaState: %v", err)
		}
		if state.Filename != "ubuntu.iso" || state.Mode != ModeCDROM {
			t.Fatalf("unexpected state: %+v", state)
		}
	})

	t.Run("ListStorageFiles", func(t *testing.T) {
		files, err := ListStorageFiles(ctx, client)
		if err != nil {
			t.Fatalf("ListStorageFiles: %v", err)
		}
		if len(files.Files) != 1 || files.Files[0].Filename != "ubuntu.iso" {
			t.Fatalf("unexpected files: %+v", files)
		}
	})

	t.Run("GetStorageSpace", func(t *testing.T) {
		space, err := GetStorageSpace(ctx, client)
		if err != nil {
			t.Fatalf("GetStorageSpace: %v", err)
		}
		if space.BytesUsed != 1 || space.BytesFree != 2 {
			t.Fatalf("unexpected space: %+v", space)
		}
	})

	t.Run("CheckMountURL", func(t *testing.T) {
		info, err := CheckMountURL(ctx, client, "http://example.com/x.iso")
		if err != nil {
			t.Fatalf("CheckMountURL: %v", err)
		}
		if !info.Usable || info.Size != 99 {
			t.Fatalf("unexpected info: %+v", info)
		}
	})
}

func TestVirtualMediaMutationRPCCalls(t *testing.T) {
	recorder := newMethodRecorder()
	_, client := deviceRPCServer(t, func(method string) any {
		recorder.record(method)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := MountWithStorage(ctx, client, "ubuntu.iso", ModeCDROM); err != nil {
		t.Fatalf("MountWithStorage: %v", err)
	}
	if err := MountWithHTTP(ctx, client, "http://example.com/x.iso", ModeDisk); err != nil {
		t.Fatalf("MountWithHTTP: %v", err)
	}
	if err := UnmountImage(ctx, client); err != nil {
		t.Fatalf("UnmountImage: %v", err)
	}
	if err := DeleteStorageFile(ctx, client, "ubuntu.iso"); err != nil {
		t.Fatalf("DeleteStorageFile: %v", err)
	}

	for _, method := range []string{"mountWithStorage", "mountWithHTTP", "unmountImage", "deleteStorageFile"} {
		if !recorder.wasCalled(method) {
			t.Errorf("expected %s to be called", method)
		}
	}
}
