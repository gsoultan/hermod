// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package factory

import (
	"errors"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/sink/txsql"
)

// newTxSQLSink builds a Transactional SQL sink. Its connection fields are the
// ones the database it names uses elsewhere -- host, port, user, password and
// dbname, or a connection string; path for SQLite -- so the form can reuse
// the database sinks' connection section.
func newTxSQLSink(conf map[string]string) (hermod.Sink, error) {
	cfg, err := txsql.ParseConfig(conf)
	if err != nil {
		return nil, err
	}
	if !hasConnectionDetails(conf, cfg.Driver) {
		return nil, errors.New("txsql: no connection details: set a connection string, or host, port, user and database" +
			" (a file path for SQLite)")
	}
	snk, err := txsql.Open(cfg, BuildConnectionString(conf, connectionType(cfg.Driver)))
	if err != nil {
		return nil, err
	}
	return snk, nil
}

// connectionType is the type BuildConnectionString knows the driver by.
func connectionType(driver string) string {
	switch driver {
	case "sqlserver":
		return "mssql"
	default:
		return driver
	}
}

func hasConnectionDetails(conf map[string]string, driver string) bool {
	for _, k := range []string{"connection_string", "uri", "url"} {
		if strings.TrimSpace(conf[k]) != "" {
			return true
		}
	}
	if driver == "sqlite" {
		return strings.TrimSpace(conf["path"]) != ""
	}
	return strings.TrimSpace(conf["host"]) != ""
}
