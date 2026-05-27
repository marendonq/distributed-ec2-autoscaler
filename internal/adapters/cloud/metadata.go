package cloud

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "net/http"
    "time"
)

const (
    imdsTokenURL = "http://169.254.169.254/latest/api/token"
    imdsBaseURL  = "http://169.254.169.254/latest/meta-data"
)

// Metadata holds a few useful EC2 metadata fields we need locally.
type Metadata struct {
    InstanceID string `json:"instance_id"`
    LocalIPv4   string `json:"local_ipv4"`
}

// fetchIMDSToken fetches an IMDSv2 token. If it fails, returns empty string and error.
func fetchIMDSToken(client *http.Client) (string, error) {
    req, err := http.NewRequest(http.MethodPut, imdsTokenURL, nil)
    if err != nil {
        return "", err
    }
    req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        body, _ := ioutil.ReadAll(resp.Body)
        return "", fmt.Errorf("token request failed: %d %s", resp.StatusCode, string(body))
    }
    tok, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return "", err
    }
    return string(tok), nil
}

// getMetadataValue retrieves a metadata path value using optional IMDSv2 token.
func getMetadataValue(client *http.Client, token, path string) (string, error) {
    url := imdsBaseURL + "/" + path
    req, err := http.NewRequest(http.MethodGet, url, nil)
    if err != nil {
        return "", err
    }
    if token != "" {
        req.Header.Set("X-aws-ec2-metadata-token", token)
    }
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        body, _ := ioutil.ReadAll(resp.Body)
        return "", fmt.Errorf("metadata request failed: %d %s", resp.StatusCode, string(body))
    }
    data, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return "", err
    }
    return string(bytes.TrimSpace(data)), nil
}

// GetInstanceMetadata attempts to read IMDSv2 metadata and returns Metadata.
// It times out quickly if not on EC2.
func GetInstanceMetadata() (*Metadata, error) {
    client := &http.Client{Timeout: 2 * time.Second}
    token, err := fetchIMDSToken(client)
    // if token fails, continue with empty token (IMDSv1 may be blocked), but try v1 without token
    _ = err

    instanceID, err := getMetadataValue(client, token, "instance-id")
    if err != nil {
        return nil, err
    }
    localIPv4, err := getMetadataValue(client, token, "local-ipv4")
    if err != nil {
        // not critical, continue with empty
        localIPv4 = ""
    }
    return &Metadata{InstanceID: instanceID, LocalIPv4: localIPv4}, nil
}

// ToJSON helper for debug/printing
func (m *Metadata) ToJSON() string {
    b, _ := json.Marshal(m)
    return string(b)
}
