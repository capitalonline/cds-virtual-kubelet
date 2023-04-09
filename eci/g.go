package eci

import (
	"os"
)

func init() {
	NodeName = os.Getenv("DEFAULT_NODE_NAME")
	if NodeName == "" {
		NodeName = "cds-virtual-node"
	}
}

var (
	SiteId    = os.Getenv("SITE_ID")
	ClusterId = os.Getenv("CLUSTER_ID")
	NodeId    = os.Getenv("DEFAULT_NODE_ID")
	NodeName  string
	PrivateId = os.Getenv("PRIVATE_ID")
	MaxPods   = os.Getenv("MAX_PODS")
)
