package model

import "encoding/json"

type Group struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}
type Monitor struct {
	ID                    string          `json:"id"`
	Name                  string          `json:"name"`
	GroupID               string          `json:"group_id"`
	Country               string          `json:"country"`
	Description           string          `json:"description,omitempty"`
	Order                 int             `json:"order"`
	OnlineUsers           *int            `json:"online_users,omitempty"`
	Public                bool            `json:"public"`
	Maintenance           bool            `json:"maintenance"`
	Paused                bool            `json:"paused"`
	Source                string          `json:"source"`
	NodeUUID              string          `json:"node_uuid"`
	SystemName            string          `json:"system_name"`
	SyncedName            string          `json:"synced_name,omitempty"`
	NodeConfigFingerprint string          `json:"node_config_fingerprint"`
	PanelState            string          `json:"panel_state"`
	NodeState             string          `json:"node_state,omitempty"`
	NodeDisabled          bool            `json:"node_disabled,omitempty"`
	Missing               bool            `json:"missing"`
	HostManaged           bool            `json:"host_managed,omitempty"`
	HostDisabled          bool            `json:"host_disabled,omitempty"`
	HostID                string          `json:"host_id"`
	HostFingerprint       string          `json:"host_fingerprint"`
	NeedsConfig           bool            `json:"needs_config"`
	Kind                  string          `json:"kind"`
	Address               string          `json:"address"`
	Port                  int             `json:"port"`
	URL                   string          `json:"url"`
	ExpectedStatus        int             `json:"expected_status"`
	ExpectedBody          string          `json:"expected_body"`
	DNSRecord             string          `json:"dns_record,omitempty"`
	ExpectedDNS           string          `json:"expected_dns,omitempty"`
	ServerName            string          `json:"server_name,omitempty"`
	CertWarnDays          int             `json:"cert_warn_days,omitempty"`
	Interval              int             `json:"interval"`
	Timeout               int             `json:"timeout"`
	Threshold             int             `json:"threshold"`
	Policy                string          `json:"policy"`
	ProbeIDs              []string        `json:"probe_ids"`
	AllProbes             bool            `json:"all_probes"`
	Revision              string          `json:"revision"`
	Outbound              json.RawMessage `json:"outbound,omitempty"`
	ProfileExpires        int64           `json:"profile_expires"`
	HasProfile            bool            `json:"has_profile"`
	RestrictionsReviewed  bool            `json:"restrictions_reviewed"`
	Created               int64           `json:"created"`
}

func (m Monitor) NodeStatus() string {
	if m.NodeState != "" {
		return m.NodeState
	}
	if m.NodeDisabled {
		return "off"
	}
	switch m.PanelState {
	case "connected":
		return "on"
	case "offline":
		return "error"
	case "disabled":
		return "off"
	}
	return ""
}

// Checkable allows checks to continue for a hidden host while its node is enabled.
func (m Monitor) Checkable() bool {
	if m.Paused || m.Missing {
		return false
	}
	if m.Source != "remnawave" {
		return true
	}
	if !m.HostManaged {
		return false
	}
	if !m.HostDisabled {
		return true
	}
	switch m.NodeStatus() {
	case "on", "error":
		return true
	}
	return false
}

// Active controls publication: a hidden host stays off the status page even
// when checks continue for its enabled node.
func (m Monitor) Active() bool {
	if m.Paused {
		return false
	}
	if m.Source != "remnawave" {
		return true
	}
	// Remnawave publishes hosts, not nodes. A host stays active independently
	// of the state of any node it is attached to.
	return m.HostManaged && !m.HostDisabled && !m.Missing
}

type Probe struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Country           string `json:"country"`
	City              string `json:"city"`
	Operator          string `json:"operator"`
	ASN               int    `json:"asn"`
	Access            string `json:"access"`
	ExpectedIP        string `json:"expected_ip"`
	Platform          string `json:"platform"`
	Heartbeat         int64  `json:"heartbeat"`
	LastPoll          int64  `json:"last_poll"`
	ObservedIP        string `json:"observed_ip"`
	ObservedASN       int    `json:"observed_asn"`
	ObservedCountry   string `json:"observed_country"`
	ObservedOperator  string `json:"observed_operator"`
	ObservedCity      string `json:"observed_city"`
	ObservedAccess    string `json:"observed_access"`
	ObservedAt        int64  `json:"observed_at"`
	ObservedDirect    bool   `json:"observed_direct"`
	ObservedVPN       bool   `json:"observed_vpn"`
	ObservedInterface string `json:"observed_interface"`
	ObservedPathError string `json:"observed_path_error"`
	VerifiedAt        int64  `json:"verified_at"`
	Healthy           bool   `json:"healthy"`
	PathVerified      bool   `json:"path_verified"`
	Revoked           bool   `json:"revoked"`
	HasToken          bool   `json:"has_token"`
}
type Observation struct {
	Platform  string `json:"platform"`
	IP        string `json:"ip"`
	ASN       int    `json:"asn"`
	Country   string `json:"country"`
	Operator  string `json:"operator"`
	City      string `json:"city"`
	Access    string `json:"access"`
	Interface string `json:"interface,omitempty"`
	VPN       bool   `json:"vpn"`
	PathError string `json:"path_error,omitempty"`
	Healthy   bool   `json:"healthy"`
	Direct    bool   `json:"direct"`
}
type Task struct {
	ID      string  `json:"id"`
	ProbeID string  `json:"probe_id"`
	Monitor Monitor `json:"monitor"`
	Issued  int64   `json:"issued"`
	Expires int64   `json:"expires"`
}
type Result struct {
	TaskID      string      `json:"task_id"`
	MonitorID   string      `json:"monitor_id"`
	ProbeID     string      `json:"probe_id"`
	Revision    string      `json:"revision"`
	Timestamp   int64       `json:"timestamp"`
	Outcome     string      `json:"outcome"`
	Stage       string      `json:"failed_stage"`
	Errno       string      `json:"errno"`
	Latency     float64     `json:"latency"`
	DNS         []string    `json:"dns"`
	TLSExpires  int64       `json:"tls_expires,omitempty"`
	Network     string      `json:"network_state"`
	ICMP        string      `json:"icmp_state"`
	Web         string      `json:"web_state"`
	VPN         string      `json:"vpn_state"`
	Observation Observation `json:"observation"`
}
type Evidence struct {
	Probe      Probe    `json:"probe"`
	State      string   `json:"state"`
	Outcome    string   `json:"outcome"`
	Stage      string   `json:"failed_stage"`
	DNS        []string `json:"dns,omitempty"`
	TLSExpires int64    `json:"tls_expires,omitempty"`
	Network    string   `json:"network_state"`
	ICMP       string   `json:"icmp_state"`
	Web        string   `json:"web_state"`
	VPN        string   `json:"vpn_state"`
	Latency    float64  `json:"latency"`
	Timestamp  int64    `json:"timestamp"`
	Stable     bool     `json:"stable"`
	Offline    bool     `json:"offline,omitempty"`
}
type Diagnosis struct {
	Status     string   `json:"status"`
	Reason     string   `json:"reason_code"`
	Confidence string   `json:"confidence"`
	Message    string   `json:"message"`
	Networks   []string `json:"networks"`
	Affected   []string `json:"affected"`
	ProbeCount int      `json:"probe_count"`
	Latency    float64  `json:"latency"`
}
type Segment struct {
	Timestamp  int64   `json:"timestamp"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason_code"`
	Latency    float64 `json:"latency"`
	Probes     int     `json:"probes"`
	Offline    int     `json:"offline,omitempty"`
	Aggregated bool    `json:"aggregated,omitempty"`
}
type MonitorView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	OnlineUsers *int   `json:"online_users,omitempty"`
	GroupID     string `json:"group_id"`
	Country     string `json:"country"`
	Kind        string `json:"kind"`
	Diagnosis
	Uptime    *float64        `json:"uptime"`
	Coverage  float64         `json:"coverage"`
	History   []Segment       `json:"history"`
	Recent    []Segment       `json:"recent"`
	LastCheck int64           `json:"last_check"`
	Metrics   []NetworkMetric `json:"metrics"`
}
type NetworkMetric struct {
	Network  string   `json:"network"`
	Metric   string   `json:"metric"`
	Uptime   *float64 `json:"uptime"`
	Coverage float64  `json:"coverage"`
}
type IncidentUpdate struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}
type Incident struct {
	ID        string           `json:"id"`
	MonitorID string           `json:"monitor_id"`
	Title     string           `json:"title"`
	Status    string           `json:"status"`
	Public    bool             `json:"public"`
	Auto      bool             `json:"auto"`
	Created   int64            `json:"created"`
	Updated   int64            `json:"updated"`
	Updates   []IncidentUpdate `json:"updates"`
}
type PanelStats struct {
	OnlineUsers  int     `json:"online_users"`
	EnabledNodes int     `json:"enabled_nodes"`
	TrafficBytes float64 `json:"traffic_bytes"`
	Updated      int64   `json:"updated"`
}
type Settings struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	PanelURL      string `json:"panel_url"`
	PanelToken    string `json:"panel_token,omitempty"`
	HasPanelToken bool   `json:"has_panel_token"`
	PanelVersion  string `json:"panel_version"`
	SyncInterval  int    `json:"sync_interval"`
	RetentionDays int    `json:"retention_days"`
	LastSync      int64  `json:"last_sync"`
	SyncError     string `json:"sync_error"`
}

// HostExclusion keeps an administrator-deleted host out of automatic imports.
type HostExclusion struct {
	UUID    string `json:"uuid"`
	Remark  string `json:"remark,omitempty"`
	Address string `json:"address,omitempty"`
	Created int64  `json:"created"`
}

type Host struct {
	UUID              string   `json:"uuid"`
	Remark            string   `json:"remark"`
	ServerDescription string   `json:"serverDescription,omitempty"`
	Address           string   `json:"address"`
	Port              int      `json:"port"`
	ViewPosition      int      `json:"viewPosition,omitempty"`
	Nodes             []string `json:"nodes"`
	Inbound           struct {
		Profile string `json:"configProfileUuid"`
		UUID    string `json:"configProfileInboundUuid"`
	} `json:"inbound"`
	Disabled     bool            `json:"isDisabled"`
	SNI          string          `json:"sni"`
	Fingerprint  string          `json:"fingerprint"`
	Path         string          `json:"path"`
	Host         string          `json:"host"`
	Security     string          `json:"securityLayer"`
	ALPN         string          `json:"alpn"`
	OverrideSNI  bool            `json:"overrideSniFromAddress"`
	KeepSNIBlank bool            `json:"keepSniBlank"`
	XHTTP        json.RawMessage `json:"xhttpExtraParams"`
	Mux          json.RawMessage `json:"muxParams"`
	Sockopt      json.RawMessage `json:"sockoptParams"`
	Mask         json.RawMessage `json:"finalMask"`
	Mapper       json.RawMessage `json:"mapper"`
	Template     string          `json:"xrayJsonTemplateUuid"`
}
