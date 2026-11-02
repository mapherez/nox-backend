//go:build !unix

package app

import (
	"fmt"
	"net"
)

func (s *Server) listenSuiteLocal() (net.Listener, func(), error) {
	return nil, nil, fmt.Errorf("local Suite transport requires a Unix host or the supported Docker deployment")
}
