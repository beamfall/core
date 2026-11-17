//go:build !linux && !darwin

package remoteaccess

import "net"

func boundTCPUnsentBacklog(conn *net.TCPConn) net.Conn { return conn }
