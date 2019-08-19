package click_mig

import (
	"fmt"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type client = *sqlx.DB

type Migrator struct {
	nodeConnections *[]NodeConnection
	clusterName     string
	dbName          string
	basePath        string
	tablePostfix    string
}

func NewMigrator(nodeList string, masterNode string, user string, password string, clusterName string, dbName string, migrationsDir string, tablePostfix string) (*Migrator, error) {
	hosts, err := getNodeConnections(nodeList, masterNode, user, password, clusterName, dbName)
	if err != nil {
		return nil, err
	}
	return &Migrator{
		nodeConnections: hosts,
		clusterName:     clusterName,
		dbName:          dbName,
		basePath:        migrationsDir,
		tablePostfix:    tablePostfix,
	}, nil
}

type migrationDescriptor struct {
	Number     int32
	Name       string
	Role       string
	NodeData   []byte
	MasterData []byte
}

type connectionBundle struct {
	client             client
	nodeName           string
	isMaster           bool
	existentMigrations map[int32]bool
}

var (
	tableRegex      = regexp.MustCompile(`%Table:([A-z_]+)%`)
	childTableRegex = regexp.MustCompile(`%ChildTable:([A-z_]+)%`)
)

func (migrator *Migrator) getParsedQuery(query string, clusterName string, dbName string, isRunOnMaster bool, masterCount int) string {
	query = strings.Replace(query, "%DB_NAME%", dbName, -1)
	query = strings.Replace(query, "%CLUSTER_NAME%", clusterName, -1)

	postfix := ""
	childPostfix := ""
	if masterCount > 0 {
		if isRunOnMaster {
			childPostfix = migrator.tablePostfix
		} else {
			postfix = migrator.tablePostfix
		}
	}

	query = tableRegex.ReplaceAllString(query, `${1}`+postfix)
	query = childTableRegex.ReplaceAllString(query, `${1}`+childPostfix)

	return query
}

func (migrator *Migrator) Apply() error {
	migrationList, err := parseMigrations(migrator.basePath)
	if err != nil {
		return fmt.Errorf("cannot apply migrations: %v", err)
	}

	cb := make([]connectionBundle, 0)

	// function to fill ConnectionBundle
	nodeCount := 0
	masterCount := 0
	for _, host := range *migrator.nodeConnections {
		c, err := connectToClickHouse(host.Url)
		if err != nil {
			return fmt.Errorf("cannot connect to node: %v", err)
		}
		if err = pingNode(c); err != nil {
			return fmt.Errorf("cannot get access to node: %v", err)
		}

		cb = append(cb, connectionBundle{
			c,
			host.NodeName,
			host.IsMaster,
			map[int32]bool{},
		})

		if host.IsMaster {
			masterCount++
		} else {
			nodeCount++
		}
	}

	// close all connections
	finalizeConnections := func(cbs *[]connectionBundle) {
		for _, cbE := range *cbs {
			if cbE.client != nil {
				if err := cbE.client.Close(); err != nil {
					log.Debugf("Cannot close connection to '%s': %v", cbE.nodeName, err)
				} else {
					log.Debugf("Connection to '%s' was closed", cbE.nodeName)
				}
			}
		}
	}
	defer finalizeConnections(&cb)

	// check if migrations exists and create tables for migrations if needed
	for _, connectionBundle := range cb {
		// check and create tables
		if !isMigrationTableExists(connectionBundle.client) {
			if err = createMigrationTable(connectionBundle.client); err != nil {
				return err
			} else {
				log.Infof("Created 'migrations' table for node '%s'", connectionBundle.nodeName)
			}
		}
		// list all migrations
		migrations, err := listMigrationEntries(connectionBundle.client)
		if err != nil {
			return fmt.Errorf("cannot get migrations list for node '%s': %v", connectionBundle.nodeName, err)
		}
		// enum migrations
		for _, migE := range *migrations {
			connectionBundle.existentMigrations[migE.Id] = true
		}
	}

	// apply migrations
	for _, migRation := range *migrationList {
		for _, connectionBundle := range cb {
			if !connectionBundle.existentMigrations[migRation.Number] {
				log.Debugf("Migration '%d_%s' isn't exists for node '%s'", migRation.Number, migRation.Name, connectionBundle.nodeName)
				// start transaction
				log.Infof("Applying migration '%d_%s' for node '%s'...", migRation.Number, migRation.Name, connectionBundle.nodeName)

				applyContent := func(content []byte, migTypeIsMaster bool) error {
					if len(content) > 0 {
						tx, _ := connectionBundle.client.Beginx()
						migrationBody := string(content)
						for _, subQuery := range strings.Split(migrationBody, ";") {
							if subQuery != "" {
								subQuery = migrator.getParsedQuery(subQuery, migrator.clusterName, migrator.dbName, migTypeIsMaster, masterCount)
								_, err := tx.Exec(subQuery)
								if err != nil {
									_ = tx.Rollback()
									return fmt.Errorf("error applying '%d_%s' for '%s'(master:%t): %v", migRation.Number, migRation.Name, connectionBundle.nodeName, migTypeIsMaster, err)
								}
							}
						}

						// Add migration to migrations table if this is not a master
						if !migTypeIsMaster {
							if err := setMigrationApplied(migRation.Number, migRation.Name, tx); err != nil {
								_ = tx.Rollback()
								return fmt.Errorf("error set-apply '%d_%s' for '%s'(master:%t): %v", migRation.Number, migRation.Name, connectionBundle.nodeName, migTypeIsMaster, err)
							}
						}

						// commit transaction
						if err := tx.Commit(); err != nil {
							_ = tx.Rollback()
							log.Errorf("Cannot apply migration '%d_%s' for '%s'(master:%t): %v", migRation.Number, migRation.Name, connectionBundle.nodeName, migTypeIsMaster, err)
							return err
						}

						log.Infof("Migration '%d_%s' was APPLIED for '%s'(master:%t)!", migRation.Number, migRation.Name, connectionBundle.nodeName, migTypeIsMaster)
					} else {
						log.Warnf("Migration '%d_%s' isn't correct for node '%s'(master:%t)", migRation.Number, migRation.Name, connectionBundle.nodeName, migTypeIsMaster)
					}

					return nil
				}

				err = applyContent(migRation.NodeData, false)
				if err != nil {
					return err
				}

				if connectionBundle.isMaster {
					err = applyContent(migRation.MasterData, true)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

func parseMigrations(basePath string) (*[]*migrationDescriptor, error) {
	files, err := filepath.Glob(basePath + "/*.node.sql")
	if err != nil {
		return nil, err
	}

	// regex
	migrationFileRegex := regexp.MustCompile("(?P<number>[0-9]{4})_(?P<name>[a-z]+).(?P<role>node|master).(?P<ext>sql)")

	// create output array
	migrationList := make([]*migrationDescriptor, 0)

	for _, migrationFilePath := range files {
		nodeContent, err := ioutil.ReadFile(migrationFilePath)
		if err != nil {
			return nil, fmt.Errorf("cannot read n-mig content of '%s': %v", migrationFilePath, err)
		}

		migrationFileName := filepath.Base(migrationFilePath)

		// check migration name validity
		fNameParts := migrationFileRegex.FindStringSubmatch(migrationFileName)
		if fNameParts == nil || len(fNameParts) != 5 {
			return nil, fmt.Errorf("cannot parse migration filename: '%s'", migrationFileName)
		}
		migrationNumber, err := strconv.Atoi(fNameParts[1])
		if err != nil {
			return nil, err
		}

		//create basic migration entry
		migrationDescriptor := migrationDescriptor{
			Number:   int32(migrationNumber),
			Name:     fNameParts[2],
			Role:     fNameParts[3],
			NodeData: nodeContent,
		}

		// check if master
		migrationMasterFileName := fmt.Sprintf("%s_%s.master.%s", fNameParts[1], fNameParts[2], fNameParts[4])
		if _, err := os.Stat(basePath + "/" + migrationMasterFileName); err == nil {
			masterContent, err := ioutil.ReadFile(basePath + "/" + migrationMasterFileName)
			if err != nil {
				return nil, fmt.Errorf("cannot read m-mig content of '%s': %v", migrationFilePath, err)
			}

			migrationDescriptor.MasterData = masterContent
		}

		// add to list
		migrationList = append(migrationList, &migrationDescriptor)
	}

	return &migrationList, nil
}
