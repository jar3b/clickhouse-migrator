package click_mig

import (
	"fmt"
	"github.com/jmoiron/sqlx"
	_ "github.com/kshvakov/clickhouse"
	"strconv"
	"strings"
)

type NodeConnection struct {
	NodeName string
	IsMaster bool
	Url      string
}

func splitHostName(node string) (string, int, error) {
	if node == "" {
		return "", 0, nil
	}
	sList := strings.Split(node, ":")
	if len(sList) != 2 {
		return "", 0, fmt.Errorf("invalid node string '%s'", node)
	}

	port, err := strconv.Atoi(sList[1])
	if err != nil {
		return "", 0, err
	}
	return sList[0], port, nil
}

func makeConnectionString(host string, port int, user string, pass string, dbName string) string {
	connectStr := fmt.Sprintf("tcp://%s:%d?database=%s", host, port, dbName)
	if user != "" {
		connectStr += "&username=" + user
	}
	if pass != "" {
		connectStr += "&password=" + pass
	}

	return connectStr
}

func getNodeConnections(nodeList string, masterNode string, user string, pass string, clusterName string, dbName string) (*[]NodeConnection, error) {
	hosts := make([]NodeConnection, 0)

	// get master cnn
	masterHost, masterPort, err := splitHostName(masterNode)
	if err != nil {
		return nil, err
	}

	nodeStrings := strings.Split(nodeList, ",")
	for _, nodeStr := range nodeStrings {
		nodeHost, nodePort, err := splitHostName(nodeStr)
		if err != nil {
			return nil, err
		}

		nodeIsMaster := (nodeHost == masterHost) && (nodePort == masterPort)
		hosts = append(hosts, NodeConnection{
			fmt.Sprintf("%s, master:%t", nodeStr, nodeIsMaster),
			nodeIsMaster,
			makeConnectionString(nodeHost, nodePort, user, pass, dbName),
		})
	}

	return &hosts, nil
}

func pingNode(client client) error {
	return client.Ping()
}

func connectToClickHouse(connectionString string) (client, error) {
	chClient, err := sqlx.Open("clickhouse", connectionString)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Clickhouse: %v", err)
	}

	return chClient, nil
}
