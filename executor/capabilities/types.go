package capabilities

// HostProfile contains discovered information about the target host.
type HostProfile struct {
	Hostname      string            `json:"hostname"`
	DistroID      string            `json:"distro_id"`
	DistroName    string            `json:"distro_name"`
	DistroVersion string            `json:"distro_version"`
	DistroFamily  string            `json:"distro_family"`
	Kernel        string            `json:"kernel"`
	KernelVersion string            `json:"kernel_version"`
	Arch          string            `json:"arch"`
	InitSystem    string            `json:"init_system"`
	PackageMgr    string            `json:"package_manager"`
	SELinux       bool              `json:"selinux"`
	AppArmor      bool              `json:"apparmor"`
	AvailableCmds []string          `json:"available_commands"`
	Extra         map[string]string `json:"extra,omitempty"`
}

// CmdResult holds the result of a command execution.
type CmdResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// IdentityGroup holds local group metadata returned by identity lookup helpers.
type IdentityGroup struct {
	Name string `json:"name"`
	GID  int    `json:"gid"`
}

// IdentityUser holds local user metadata returned by identity lookup helpers.
type IdentityUser struct {
	Name         string   `json:"name"`
	UID          int      `json:"uid"`
	GID          int      `json:"gid"`
	Comment      string   `json:"comment,omitempty"`
	Home         string   `json:"home,omitempty"`
	Shell        string   `json:"shell,omitempty"`
	PrimaryGroup string   `json:"primary_group,omitempty"`
	Groups       []string `json:"groups,omitempty"`
}

// FileStat holds metadata about a file.
type FileStat struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"`
	UID     uint32 `json:"uid"`
	GID     uint32 `json:"gid"`
	Owner   string `json:"owner,omitempty"`
	Group   string `json:"group,omitempty"`
	ModTime int64  `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
	Digest  string `json:"digest"`
}
