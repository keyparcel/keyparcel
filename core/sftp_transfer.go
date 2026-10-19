package core

// Match pkg/sftp's default 64-request window of 32 KiB packets. Larger Read and
// Write calls let the library pipeline requests without bypassing byte observers.
func sftpTransferBufferSize(size int64) int {
	const window = 64 * 32 * 1024
	if size > 0 && size < window {
		return int(size)
	}
	return window
}
