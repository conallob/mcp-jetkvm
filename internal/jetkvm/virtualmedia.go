package jetkvm

import "context"

// VirtualMediaSource identifies where a mounted image's bytes come from.
type VirtualMediaSource string

const (
	SourceHTTP    VirtualMediaSource = "HTTP"
	SourceStorage VirtualMediaSource = "Storage"
)

// VirtualMediaMode selects how a mounted image is presented to the target
// host's USB controller.
type VirtualMediaMode string

const (
	ModeCDROM VirtualMediaMode = "CDROM"
	ModeDisk  VirtualMediaMode = "Disk"
)

// VirtualMediaState describes the image currently mounted as a virtual USB
// boot device, if any.
type VirtualMediaState struct {
	Source   VirtualMediaSource `json:"source"`
	Mode     VirtualMediaMode   `json:"mode"`
	Filename string             `json:"filename,omitempty"`
	URL      string             `json:"url,omitempty"`
	Size     int64              `json:"size"`
}

// VirtualMediaURLInfo reports whether a remote URL can be mounted directly.
type VirtualMediaURLInfo struct {
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
	Size   int64  `json:"size"`
}

// StorageFile is a single image file stored on the device.
type StorageFile struct {
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// StorageFiles lists the images stored on the device.
type StorageFiles struct {
	Files []StorageFile `json:"files"`
}

// StorageSpace reports the device's local image storage usage.
type StorageSpace struct {
	BytesUsed int64 `json:"bytesUsed"`
	BytesFree int64 `json:"bytesFree"`
}

// GetVirtualMediaState returns the currently mounted virtual media image, if any.
func GetVirtualMediaState(ctx context.Context, client *Client) (*VirtualMediaState, error) {
	var state VirtualMediaState
	if err := callRPC(ctx, client, "getVirtualMediaState", nil, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// ListStorageFiles lists the image files stored on the device.
func ListStorageFiles(ctx context.Context, client *Client) (*StorageFiles, error) {
	var files StorageFiles
	if err := callRPC(ctx, client, "listStorageFiles", nil, &files); err != nil {
		return nil, err
	}
	return &files, nil
}

// GetStorageSpace returns free/used byte counts for the device's local image storage.
func GetStorageSpace(ctx context.Context, client *Client) (*StorageSpace, error) {
	var space StorageSpace
	if err := callRPC(ctx, client, "getStorageSpace", nil, &space); err != nil {
		return nil, err
	}
	return &space, nil
}

// CheckMountURL asks the device whether a remote URL can be mounted directly
// (without first downloading it into local storage).
func CheckMountURL(ctx context.Context, client *Client, url string) (*VirtualMediaURLInfo, error) {
	var info VirtualMediaURLInfo
	if err := callRPC(ctx, client, "checkMountUrl", map[string]any{"url": url}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// MountWithStorage mounts an image already present in the device's local
// storage as a virtual USB boot device, in the given mode.
func MountWithStorage(ctx context.Context, client *Client, filename string, mode VirtualMediaMode) error {
	return callRPC(ctx, client, "mountWithStorage", map[string]any{
		"filename": filename,
		"mode":     mode,
	}, nil)
}

// MountWithHTTP mounts a remote image over HTTP as a virtual USB boot
// device, in the given mode, without downloading it into local storage first.
func MountWithHTTP(ctx context.Context, client *Client, url string, mode VirtualMediaMode) error {
	return callRPC(ctx, client, "mountWithHTTP", map[string]any{
		"url":  url,
		"mode": mode,
	}, nil)
}

// UnmountImage unmounts the currently mounted virtual media image.
func UnmountImage(ctx context.Context, client *Client) error {
	return callRPC(ctx, client, "unmountImage", nil, nil)
}

// DeleteStorageFile removes an image file from the device's local storage.
func DeleteStorageFile(ctx context.Context, client *Client, filename string) error {
	return callRPC(ctx, client, "deleteStorageFile", map[string]any{"filename": filename}, nil)
}
