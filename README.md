# clickhouse-migrator

## Install

```bash
go get https://github.com/jar3b/clickhouse-migrator
```

## Migrations

You need subfolders with migration files in format `<id>-<name>.<role>.sql`, where:

1. `id` is migration identifier 4-digit including trailing `0`, example: `0001`
2. `name` is migration name. Text field, must pass a regex `[A-z_0-9]+`, example `initial`
3. `role` is role of node which need this migrations. Choices are: `master` or `node`. Be care! 
`node` migrations also applied in `master`; is common case, `master` migrations contains `Distributed`
table descriptions.

Example of `0001-initial.master.sql`:

```sql
DROP TABLE IF EXISTS %Table:test%;
CREATE TABLE %Table:test%(
  event_date Date,
  event_time DateTime,
  event_type UInt8,
  event_data FixedString(17),
) engine=Distributed(%CLUSTER_NAME%, %DB_NAME%, %ChildTable:test%, event_time);
```

Example of `0001-initial.node.sql`

```sql
DROP TABLE IF EXISTS %Table:test%;
CREATE TABLE %Table:test%(
  event_date Date,
  event_time DateTime,
  event_type UInt8,
  event_data FixedString(17),
) engine=MergeTree(event_date, (event_time, event_type, event_data), 8192);
```

As you can see, you can use these "helpers":

- `%Table:<table_name>%` table name, good for per-node tables if you use `Distributed` tables. These tables will be postfixed 
with value, described below, BUT only if table is referenced by `Distributed` table. If you dont use them, you must always use 
 `%Table:<table_name>%` definition or specify table name as-is (`DROP TABLE IF EXISTS test;`)
- `%ChildTable:test%` - reference for a regular table name in `Distributed` table definition. This is a kind of link to a regular table.
- `%CLUSTER_NAME%` - name of ClickHouse cluster (if it was used).
- `%DB_NAME%` - database name.

## Code usage

```go
package main

import (
	clickhouseMigrator "github.com/jar3b/clickhouse-migrator"
	log "github.com/sirupsen/logrus"
)

func main() {
	mig, err := clickhouseMigrator.NewMigrator(
		conf.Clickhouse.NodeList,       // list of node strings (host:port)
		conf.Clickhouse.MasterNode,     // master node address (host:port)
		conf.Clickhouse.User,
		conf.Clickhouse.Pass,
		conf.Clickhouse.ClusterName,
		conf.Clickhouse.DbName,
		"migrations",                   // directory with migrations
		"_impl",                        // it is a regular table postfix, if "Distributed" was used
	)
	if err != nil {
		log.Fatalf("cannot create migrator: %v", err)
	}
	if err = mig.Apply(); err != nil {
		log.Fatalf("cannot apply migrations: %v", err)
	}
	log.Info("Migrations applied")
}
```

where conf is

```
Clickhouse struct {
    NodeList     string `envconfig:"NODE_LIST" required:"true"`
    MasterNode   string `envconfig:"MASTER_NODE" required:"true"`
    User         string `envconfig:"USER" required:"false"`
    Pass         string `envconfig:"PASS" required:"false"`
    ClusterName  string `envconfig:"CLUSTER_NAME" required:"true"`
    DbName       string `envconfig:"DB" required:"true"`
    ReplicaCount int    `envconfig:"REPLICA_COUNT" required:"true"`
}
```

and env

```
CLICKHOUSE_NODE_LIST=clickhouse:8090,clickhouse:8091,clickhouse:8092
CLICKHOUSE_MASTER_NODE=clickhouse:8090
CLICKHOUSE_USER=default
CLICKHOUSE_PASS=<password>
CLICKHOUSE_DB=my_database
CLICKHOUSE_REPLICA_COUNT=3
CLICKHOUSE_CLUSTER_NAME=default_cluster
```

## Limitations

1. Only one master node is supported 