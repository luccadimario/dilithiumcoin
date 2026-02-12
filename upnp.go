package main

import (
	"fmt"
	"strconv"

	"github.com/jcuga/go-upnp"
)

// SetupUPnP attempts to forward ports automatically
func SetupUPnP(port string) (string, error) {
	fmt.Println("Attempting UPnP port forwarding...")
	
	// Discover UPnP-enabled router
	d, err := upnp.Discover()
	if err != nil {
		fmt.Println("UPnP discovery failed (router may not support UPnP)")
		return "", err
	}
	
	// Get external IP
	externalIP, err := d.ExternalIP()
	if err != nil {
		fmt.Println("Failed to get external IP")
		return "", err
	}
	
	// Convert port string to uint16
	p, _ := strconv.ParseUint(port, 10, 16)
	portNum := uint16(p)
	
	// Forward the port (TCP and UDP)
	err = d.Forward(portNum, "Dilithium Node P2P", "TCP")
	if err != nil {
		fmt.Printf("Failed to forward port: %v\n", err)
		return externalIP, err
	}
	
	fmt.Printf("UPnP port forwarding successful!\n")
	fmt.Printf("   External IP: %s\n", externalIP)
	fmt.Printf("   Port: %s\n", port)
	
	return externalIP, nil
}

