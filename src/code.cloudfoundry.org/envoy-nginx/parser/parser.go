/* Faker envoy.exe */
package parser

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v2"
)

const DefaultSDSCredsFile = "C:\\etc\\cf-assets\\envoy_config\\sds-server-cert-and-key.yaml"

var tmpdir string = os.TempDir()

/*
* Try to use this auth_v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2/auth"?
 */
type sds struct {
	Resources []Resource `yaml:"resources,omitempty"`
}
type Resource struct {
	TLSCertificate TLSCertificate `yaml:"tls_certificate,omitempty"`
}
type TLSCertificate struct {
	CertChain  CertChain  `yaml:"certificate_chain,omitempty"`
	PrivateKey PrivateKey `yaml:"private_key,omitempty"`
}

type CertChain struct {
	InlineString string `yaml:"inline_string,omitempty"`
}

type PrivateKey struct {
	InlineString string `yaml:"inline_string,omitempty"`
}

/* Convert windows paths to unix paths */
func convertToUnixPath(path string) string {
	path = strings.Replace(path, "C:", "", -1)
	path = strings.Replace(path, "\\", "/", -1)
	return path
}

/* Parses the Envoy SDS file and extracts the cert and key */
func getCertAndKey(certsFile string) (cert, key string, err error) {
	contents, err := ioutil.ReadFile(certsFile)
	if err != nil {
		return "", "", err
	}

	auth := sds{}

	if err := yaml.Unmarshal(contents, &auth); err != nil {
		return "", "", err
	}

	cert = auth.Resources[0].TLSCertificate.CertChain.InlineString
	key = auth.Resources[0].TLSCertificate.PrivateKey.InlineString
	return cert, key, nil
}

/* Generates NGINX config file and returns its full file path.
 *  There's aleady an nginx.conf in the blob but it's just a placeholder.
 */

// later TODO: read port mapping from envoy.yaml
func GenerateConf() (string, error) {
	timestamp := time.Now().UnixNano()
	certFile := filepath.Join(tmpdir, fmt.Sprintf("cert_%d.pem", timestamp))
	keyFile := filepath.Join(tmpdir, fmt.Sprintf("key_%d.pem", timestamp))
	pidFile := filepath.Join(tmpdir, fmt.Sprintf("nginx_%d.pid", timestamp))
	confTemplate := fmt.Sprintf(`
worker_processes  1;
daemon off;

error_log stderr;
pid %s;

events {
    worker_connections  1024;
}


stream {

    upstream app {
      server 127.0.0.1:8080;
    }

    upstream sshd {
      server 127.0.0.1:2222;
    }

    server {
        listen 61001 ssl;
        ssl_certificate      %s;
        ssl_certificate_key  %s;
				proxy_pass app;
    }

    server {
        listen 61002 ssl;
        ssl_certificate      %s;
        ssl_certificate_key  %s;
				proxy_pass sshd;
    }
}
`, convertToUnixPath(pidFile),
		convertToUnixPath(certFile),
		convertToUnixPath(keyFile),
		convertToUnixPath(certFile),
		convertToUnixPath(keyFile))

	certsFile := os.Getenv("SDSCredsFile")
	if certsFile == "" {
		certsFile = DefaultSDSCredsFile
	}
	cert, key, err := getCertAndKey(certsFile)
	if err != nil {
		return "", err
	}

	err = ioutil.WriteFile(certFile, []byte(cert), 0644)
	if err != nil {
		return "", err
	}

	err = ioutil.WriteFile(keyFile, []byte(key), 0644)
	if err != nil {
		return "", err
	}

	confFile := filepath.Join(tmpdir, "envoy_nginx.conf")
	err = ioutil.WriteFile(confFile, []byte(confTemplate), 0644)
	if err != nil {
		return "", err
	}
	return confFile, nil
}