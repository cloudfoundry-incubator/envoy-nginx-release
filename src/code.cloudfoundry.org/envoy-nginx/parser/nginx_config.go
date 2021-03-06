package parser

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"text/template"
)

const FilePerm = 0644

type BaseTemplate struct {
	UpstreamAddress string
	UpstreamPort    string
	ListenerPort    string
	Name            string
	Key             string
	Cert            string
	TrustedCA       string
	MTLS            bool
	Ciphers         string
}

type envoyConfParser interface {
	ReadUnmarshalEnvoyConfig(envoyConfFile string) (EnvoyConf, error)
	GetClusters(conf EnvoyConf) ([]Cluster, map[string]PortAndCiphers)
	GetMTLS(conf EnvoyConf) bool
}

type sdsCredParser interface {
	GetCertAndKey() (string, string, error)
}

type sdsValidationParser interface {
	GetCACert() (string, error)
}

type NginxConfig struct {
	envoyConfParser     envoyConfParser
	sdsCredParser       sdsCredParser
	sdsValidationParser sdsValidationParser
	nginxDir            string
	confFile            string
	certFile            string
	keyFile             string
	trustedCAFile       string
	pidFile             string
}

func NewNginxConfig(envoyConfParser envoyConfParser, sdsCredParser sdsCredParser, sdsValidationParser sdsValidationParser, nginxDir string) NginxConfig {
	return NginxConfig{
		envoyConfParser:     envoyConfParser,
		sdsCredParser:       sdsCredParser,
		sdsValidationParser: sdsValidationParser,
		nginxDir:            nginxDir,
		confFile:            filepath.Join(nginxDir, "conf", "nginx.conf"),
		certFile:            filepath.Join(nginxDir, "cert.pem"),
		keyFile:             filepath.Join(nginxDir, "key.pem"),
		trustedCAFile:       filepath.Join(nginxDir, "ca.pem"),
		pidFile:             filepath.Join(nginxDir, "nginx.pid"),
	}
}

func (n NginxConfig) GetNginxDir() string {
	return n.nginxDir
}

func (n NginxConfig) GetConfFile() string {
	return n.confFile
}

// Convert windows paths to unix paths
func convertToUnixPath(path string) string {
	path = strings.Replace(path, "C:", "", -1)
	path = strings.Replace(path, "\\", "/", -1)
	return path
}

// Generates NGINX config file.
func (n NginxConfig) Generate(envoyConfFile string) error {
	envoyConf, err := n.envoyConfParser.ReadUnmarshalEnvoyConfig(envoyConfFile)
	if err != nil {
		return fmt.Errorf("read and unmarshal Envoy config: %s", err)
	}

	clusters, nameToPortAndCiphersMap := n.envoyConfParser.GetClusters(envoyConf)

	const baseTemplate = `
    upstream {{.Name}} {
      server {{.UpstreamAddress}}:{{.UpstreamPort}};
    }

    server {
        listen {{.ListenerPort}} ssl;
        ssl_certificate        {{.Cert}};
        ssl_certificate_key    {{.Key}};
        {{ if .MTLS }}
        ssl_client_certificate {{.TrustedCA}};
        ssl_verify_client on;
        {{ end }}
        proxy_pass {{.Name}};

				ssl_prefer_server_ciphers on;
				ssl_ciphers {{.Ciphers}};
    }
	`
	//create buffer to store template output
	out := &bytes.Buffer{}

	//Create a new template and parse the conf template into it
	t := template.Must(template.New("baseTemplate").Parse(baseTemplate))

	unixCert := convertToUnixPath(n.certFile)
	unixKey := convertToUnixPath(n.keyFile)
	unixCA := convertToUnixPath(n.trustedCAFile)

	mtlsEnabled := n.envoyConfParser.GetMTLS(envoyConf)

	//Execute the template for each socket address
	for _, c := range clusters {
		listenerPortAndCiphers, exists := nameToPortAndCiphersMap[c.Name]
		if !exists {
			return fmt.Errorf("port is missing for cluster name %s", c.Name)
		}

		bts := BaseTemplate{
			Name:            c.Name,
			UpstreamAddress: c.LoadAssignment.Endpoints[0].LBEndpoints[0].Endpoint.Address.SocketAddress.Address,
			UpstreamPort:    c.LoadAssignment.Endpoints[0].LBEndpoints[0].Endpoint.Address.SocketAddress.PortValue,
			Cert:            unixCert,
			Key:             unixKey,
			TrustedCA:       unixCA,
			MTLS:            mtlsEnabled,

			ListenerPort: listenerPortAndCiphers.Port,
			Ciphers:      listenerPortAndCiphers.Ciphers,
		}

		err = t.Execute(out, bts)
		if err != nil {
			return fmt.Errorf("executing envoy-nginx config template: %s", err)
		}
	}

	confTemplate := fmt.Sprintf(`
worker_processes  1;
daemon on;

error_log logs/error.log;
pid %s;

events {
    worker_connections  1024;
}

stream {
	%s
}
`, convertToUnixPath(n.pidFile),
		out)

	err = ioutil.WriteFile(n.confFile, []byte(confTemplate), FilePerm)
	if err != nil {
		return fmt.Errorf("%s - write file failed: %s", n.confFile, err)
	}

	return nil
}

func (n NginxConfig) WriteTLSFiles() error {
	cert, key, err := n.sdsCredParser.GetCertAndKey()
	if err != nil {
		return fmt.Errorf("get cert and key from sds server cred parser: %s", err)
	}

	err = ioutil.WriteFile(n.certFile, []byte(cert), FilePerm)
	if err != nil {
		return fmt.Errorf("write cert: %s", err)
	}

	err = ioutil.WriteFile(n.keyFile, []byte(key), FilePerm)
	if err != nil {
		return fmt.Errorf("write key: %s", err)
	}

	caCert, err := n.sdsValidationParser.GetCACert()
	if err != nil {
		return fmt.Errorf("get ca cert from sds server validation parser: %s", err)
	}

	// If there is no CA Cert, do not write the ca.pem.
	if len(caCert) == 0 {
		return nil
	}

	err = ioutil.WriteFile(n.trustedCAFile, []byte(caCert), FilePerm)
	if err != nil {
		return fmt.Errorf("write ca cert file: %s", err)
	}

	return nil
}
