package entities

type NQERunResult struct {
	SnapshotID string                   `json:"snapshotId"`
	Items      []map[string]interface{} `json:"items"`
}

type NQEQuery struct {
	QueryID    string `json:"queryId"`
	Path       string `json:"path"`
	Intent     string `json:"intent"`
	Repository string `json:"repository"`
}

type NQEOrgQuerySummary struct {
	Path          string `json:"path"`
	LastCommitId  string `json:"lastCommitId"`
	QueryID       string `json:"queryId"`
	SourceCodeSha string `json:"sourceCodeSha"`
}

type NQEOrgQueriesResponse struct {
	Queries        []NQEOrgQuerySummary `json:"queries"`
	AccessSettings []interface{}        `json:"accessSettings"`
}

type NQECommitInfo struct {
	ID          string `json:"id"`
	AuthorEmail string `json:"authorEmail"`
	CommittedAt int64  `json:"committedAt"`
	Title       string `json:"title"`
	Body        string `json:"body"`
}

type NQEQueryDetail struct {
	QueryID       string        `json:"queryId"`
	Path          string        `json:"path"`
	SourceCode    string        `json:"sourceCode"`
	Intent        string        `json:"intent"`
	Description   string        `json:"description"`
	SourceCodeSha string        `json:"sourceCodeSha"`
	CommitCount   int           `json:"commitCount"`
	LastCommit    NQECommitInfo `json:"lastCommit"`
	FirstCommit   NQECommitInfo `json:"firstCommit"`
	Repository    string        `json:"repository"`
}

type NQEDiffRequest struct {
	QueryID    string                 `json:"queryId"`
	CommitID   string                 `json:"commitId,omitempty"`
	Options    *NQEQueryOptions       `json:"options,omitempty"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

type NQEDiffResult struct {
	TotalNumRows int                      `json:"totalNumRows"`
	Rows         []map[string]interface{} `json:"rows"`
}

type NQEQueryOptions struct {
	DeviceFilter    string `json:"deviceFilter,omitempty"`
	LocationFilter  string `json:"locationFilter,omitempty"`
	MaxResults      int    `json:"maxResults,omitempty"`
	IncludeMetadata bool   `json:"includeMetadata,omitempty"`
}

type NQEQueryParams struct {
	QueryString string                 `json:"queryString,omitempty"`
	QueryID     string                 `json:"queryId,omitempty"`
	NetworkID   string                 `json:"networkId,omitempty"`
	SnapshotID  string                 `json:"snapshotId,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Options     *NQEQueryOptions       `json:"options,omitempty"`
}

type DeviceQueryParams struct {
	SnapshotID string `json:"snapshotId,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type DeviceResponse struct {
	Devices    []Device `json:"devices"`
	TotalCount int      `json:"totalCount"`
}

type Device struct {
	Name          string                 `json:"name"`
	Type          string                 `json:"type,omitempty"`
	Vendor        string                 `json:"vendor,omitempty"`
	OSVersion     string                 `json:"osVersion,omitempty"`
	Platform      string                 `json:"platform,omitempty"`
	Model         string                 `json:"model,omitempty"`
	ManagementIPs []string               `json:"managementIps,omitempty"`
	Hostname      string                 `json:"hostname,omitempty"`
	Version       string                 `json:"version,omitempty"`
	SerialNumber  string                 `json:"serialNumber,omitempty"`
	LocationID    string                 `json:"locationId,omitempty"`
	Interfaces    []DeviceInterface      `json:"interfaces,omitempty"`
	Properties    map[string]interface{} `json:"properties,omitempty"`
}

type DeviceInterface struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IPAddress   string `json:"ipAddress,omitempty"`
	Status      string `json:"status,omitempty"`
	Type        string `json:"type,omitempty"`
}

type Snapshot struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	NetworkID          string `json:"networkId"`
	DeviceCount        int    `json:"deviceCount"`
	CreationTimestamp  int64  `json:"creationTimestamp"`
	SnapshotStartTime  int64  `json:"snapshotStartTime"`
	SnapshotFinishTime int64  `json:"snapshotFinishTime"`
	SnapshotDuration   int    `json:"snapshotDuration"`
	CreationType       string `json:"creationType"`
}

type Network struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"createdAt"`
}

type NetworkUpdate struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
}

type Location struct {
	ID          string `json:"id"`
	NetworkID   string `json:"networkId"`
	Name        string `json:"name"`
	ParentID    string `json:"parentId,omitempty"`
	Description string `json:"description,omitempty"`
	Latitude    string `json:"latitude,omitempty"`
	Longitude   string `json:"longitude,omitempty"`
	Address     string `json:"address,omitempty"`
	Country     string `json:"country,omitempty"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
}

type LocationCreate struct {
	Name        string `json:"name"`
	ParentID    string `json:"parentId,omitempty"`
	Description string `json:"description,omitempty"`
	Latitude    string `json:"latitude,omitempty"`
	Longitude   string `json:"longitude,omitempty"`
	Address     string `json:"address,omitempty"`
	Country     string `json:"country,omitempty"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
}

type LocationUpdate struct {
	Name        string `json:"name,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	Description string `json:"description,omitempty"`
	Latitude    string `json:"latitude,omitempty"`
	Longitude   string `json:"longitude,omitempty"`
	Address     string `json:"address,omitempty"`
	Country     string `json:"country,omitempty"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
}

type LocationBulkPatch struct {
	Name      string            `json:"name"`
	Latitude  string            `json:"latitude,omitempty"`
	Longitude string            `json:"longitude,omitempty"`
	Address   string            `json:"address,omitempty"`
	Country   string            `json:"country,omitempty"`
	Region    string            `json:"region,omitempty"`
	City      string            `json:"city,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

type PathSearchParams struct {
	SourceDevice      string `json:"sourceDevice"`
	DestinationDevice string `json:"destinationDevice"`
	Protocol          string `json:"protocol,omitempty"`
	SourcePort        string `json:"sourcePort,omitempty"`
	DestinationPort   string `json:"destinationPort,omitempty"`
	TTL               int    `json:"ttl,omitempty"`
}

type PathSearchResponse struct {
	Paths []Path `json:"paths"`
}

type PathSearchBulkRequest struct {
	Queries []PathSearchParams `json:"queries"`
}

type PathSearchBulkResponse struct {
	Paths []Path `json:"paths"`
	Error string `json:"error,omitempty"`
}

type Path struct {
	Hops []Hop `json:"hops"`
}

type Hop struct {
	Device    string `json:"device"`
	Interface string `json:"interface"`
	SourceIP  string `json:"sourceIp"`
	DestIP    string `json:"destIp"`
	Protocol  string `json:"protocol"`
	Metric    int    `json:"metric"`
	ACLMatch  bool   `json:"aclMatch"`
	ACLName   string `json:"aclName,omitempty"`
	Zone      string `json:"zone,omitempty"`
}

type ChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ChatMessage          `json:"messages"`
	Stream   bool                   `json:"stream"`
	Extra    map[string]interface{} `json:"extra,omitempty"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Content     string `json:"content"`
	Model       string `json:"model"`
	StopReason  string `json:"stopReason"`
	StopTokenID int    `json:"stopTokenId"`
}
