#!/usr/bin/env python3
"""MySQL backup script for managed_agent database.
Dumps all table schemas and data to the backup directory.
"""

import os
import sys
from datetime import datetime, timezone

import pymysql

# --- Config from .env ---
DB_HOST = "124.221.28.203"
DB_PORT = 3306
DB_USER = "managed"
DB_PASSWORD = "managedAgent123"
DB_NAME = "managed_agent"

# Backup output directory
# meta-harness-ext is a sibling of the meta-harness project root
BACKUP_DIR = os.path.join(
    os.path.dirname(os.path.abspath(__file__)),
    "..",  # scripts/
    "..",  # meta-harness/
    "meta-harness-ext",
    "mysql-backup",
)


def connect():
    return pymysql.connect(
        host=DB_HOST,
        port=DB_PORT,
        user=DB_USER,
        password=DB_PASSWORD,
        database=DB_NAME,
        charset="utf8mb4",
        cursorclass=pymysql.cursors.Cursor,
    )


def get_tables(cursor):
    cursor.execute("SHOW TABLES")
    return [row[0] for row in cursor.fetchall()]


def dump_schema_and_data(conn, tables, outfile):
    cursor = conn.cursor()

    outfile.write("-- MySQL dump for database: {}\n".format(DB_NAME))
    outfile.write("-- Generated: {}\n".format(datetime.now(timezone.utc).isoformat()))
    outfile.write("-- Host: {}:{}\n\n".format(DB_HOST, DB_PORT))
    outfile.write("SET NAMES utf8mb4;\n")
    outfile.write("SET FOREIGN_KEY_CHECKS = 0;\n\n")

    for table in tables:
        print(f"  Dumping table: {table}")

        # Schema
        cursor.execute("SHOW CREATE TABLE `{}`".format(table))
        create = cursor.fetchone()[1]
        outfile.write("--\n")
        outfile.write("-- Table structure for `{}`\n".format(table))
        outfile.write("--\n")
        outfile.write("DROP TABLE IF EXISTS `{}`;\n".format(table))
        outfile.write(create + ";\n\n")

        # Data
        cursor.execute("SELECT COUNT(*) FROM `{}`".format(table))
        row_count = cursor.fetchone()[0]
        print(f"    {row_count} rows")

        if row_count == 0:
            outfile.write("--\n")
            outfile.write("-- No data for `{}`\n".format(table))
            outfile.write("--\n\n")
            continue

        cursor.execute("SELECT * FROM `{}`".format(table))
        rows = cursor.fetchall()
        col_names = [desc[0] for desc in cursor.description]

        outfile.write("--\n")
        outfile.write("-- Dumping data for `{}`\n".format(table))
        outfile.write("--\n")
        outfile.write("LOCK TABLES `{}` WRITE;\n".format(table))

        # Batch INSERT statements for performance (500 rows per INSERT)
        batch_size = 500
        for i in range(0, len(rows), batch_size):
            batch = rows[i : i + batch_size]
            values_list = []
            for row in batch:
                values = []
                for val in row:
                    if val is None:
                        values.append("NULL")
                    elif isinstance(val, bytes):
                        values.append("_binary '{}'".format(
                            val.decode("utf-8", errors="replace").replace("\\", "\\\\").replace("'", "\\'")
                        ))
                    elif isinstance(val, str):
                        values.append("'{}'".format(
                            val.replace("\\", "\\\\").replace("'", "\\'")
                        ))
                    elif isinstance(val, (int, float)):
                        values.append(str(val))
                    elif isinstance(val, datetime):
                        values.append("'{}'".format(val.strftime("%Y-%m-%d %H:%M:%S")))
                    else:
                        values.append("'{}'".format(str(val).replace("\\", "\\\\").replace("'", "\\'")))
                values_list.append("(" + ", ".join(values) + ")")

            outfile.write(
                "INSERT INTO `{}` (`{}`) VALUES\n{};\n".format(
                    table,
                    "`, `".join(col_names),
                    ",\n".join(values_list),
                )
            )

        outfile.write("UNLOCK TABLES;\n\n")

    outfile.write("SET FOREIGN_KEY_CHECKS = 1;\n")
    cursor.close()


def main():
    # Resolve backup dir
    backup_dir = os.path.abspath(BACKUP_DIR)
    os.makedirs(backup_dir, exist_ok=True)

    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    output_path = os.path.join(backup_dir, f"managed_agent_backup_{timestamp}.sql")

    print(f"Connecting to MySQL at {DB_HOST}:{DB_PORT} ...")
    conn = connect()
    try:
        tables = get_tables(conn.cursor())
        print(f"Found {len(tables)} tables: {', '.join(tables)}")
        print(f"Writing backup to: {output_path}")

        with open(output_path, "w", encoding="utf-8") as f:
            dump_schema_and_data(conn, tables, f)

        size_mb = os.path.getsize(output_path) / (1024 * 1024)
        print(f"\nBackup complete: {output_path} ({size_mb:.2f} MB)")
    finally:
        conn.close()


if __name__ == "__main__":
    main()