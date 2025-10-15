#!/bin/bash

# Script to clean up and delete all test data from databases
# This script drops all tables in the public schema
#
# **IMPORTANT: This script must be run on the PPROX VM**
# The databases are only accessible from the PPROX VM IP due to firewall rules
#
# To run this script:
# 1. Transfer to VM: gcloud compute scp tests/cleanup_databases.sh pprox-server:~/ --zone=$GCP_ZONE
# 2. SSH to VM: gcloud compute ssh pprox-server --zone=$GCP_ZONE
# 3. Run: chmod +x cleanup_databases.sh && ./cleanup_databases.sh

# Load environment variables
source ~/pprox-setup.env

echo "=========================================="
echo "DATABASE CLEANUP - DELETE ALL TEST DATA"
echo "=========================================="
echo ""
echo "⚠️  WARNING: This will DELETE ALL TABLES and DATA from:"
echo "   1. RDS Writer: $RDS_ENDPOINT"
echo "   2. Cloud SQL Writer: $CLOUDSQL_WRITER_HOST"
echo "   3. Cloud SQL Reader: $CLOUDSQL_READER_HOST"
echo ""
echo "This action is IRREVERSIBLE!"
echo ""
read -p "Are you sure you want to continue? Type 'DELETE ALL' to confirm: " confirm

if [ "$confirm" != "DELETE ALL" ]; then
    echo "Cleanup cancelled."
    exit 0
fi

echo ""
echo "Starting cleanup..."
echo ""

# Function to drop all tables in a database
drop_all_tables() {
    local host=$1
    local password=$2
    local db_name=$3
    
    echo "Cleaning up: $db_name"
    echo "  Host: $host"
    
    export PGPASSWORD="$password"
    export PGSSLMODE=require
    
    # Get list of tables
    tables=$(psql -h "$host" -p 5432 -U postgres -d postgres -t -c "
        SELECT tablename 
        FROM pg_tables 
        WHERE schemaname='public'
    " 2>&1)
    
    if [ $? -ne 0 ]; then
        echo "  ❌ Failed to connect or query database"
        echo "  Error: $tables"
        return 1
    fi
    
    if [ -z "$tables" ]; then
        echo "  ℹ️  No tables found (already clean)"
        return 0
    fi
    
    # Drop all tables
    echo "  Dropping tables..."
    psql -h "$host" -p 5432 -U postgres -d postgres <<EOF
-- Drop all tables in public schema
DO \$\$ DECLARE
    r RECORD;
BEGIN
    -- Drop all tables
    FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
        EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(r.tablename) || ' CASCADE';
    END LOOP;
    
    -- Drop all sequences
    FOR r IN (SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = 'public') LOOP
        EXECUTE 'DROP SEQUENCE IF EXISTS public.' || quote_ident(r.sequence_name) || ' CASCADE';
    END LOOP;
    
    -- Drop all views
    FOR r IN (SELECT table_name FROM information_schema.views WHERE table_schema = 'public') LOOP
        EXECUTE 'DROP VIEW IF EXISTS public.' || quote_ident(r.table_name) || ' CASCADE';
    END LOOP;
END \$\$;
EOF
    
    if [ $? -eq 0 ]; then
        echo "  ✅ Cleanup complete"
        return 0
    else
        echo "  ❌ Cleanup failed"
        return 1
    fi
}

# Clean RDS Writer
echo "1. Cleaning RDS Writer"
echo "------------------------------------------"
drop_all_tables "$RDS_ENDPOINT" "$RDS_PASSWORD" "RDS Writer"
RDS_STATUS=$?
echo ""

# Clean Cloud SQL Writer
echo "2. Cleaning Cloud SQL Writer"
echo "------------------------------------------"
drop_all_tables "$CLOUDSQL_WRITER_HOST" "$CLOUDSQL_PASSWORD" "Cloud SQL Writer"
CLOUDSQL_WRITER_STATUS=$?
echo ""

# Clean Cloud SQL Reader (if different from writer)
echo "3. Cleaning Cloud SQL Reader"
echo "------------------------------------------"
if [ "$CLOUDSQL_READER_HOST" != "$CLOUDSQL_WRITER_HOST" ]; then
    drop_all_tables "$CLOUDSQL_READER_HOST" "$CLOUDSQL_PASSWORD" "Cloud SQL Reader"
    CLOUDSQL_READER_STATUS=$?
else
    echo "  ℹ️  Reader is same as writer (replica will sync automatically)"
    CLOUDSQL_READER_STATUS=0
fi
echo ""

# Summary
echo "=========================================="
echo "CLEANUP SUMMARY"
echo "=========================================="
if [ $RDS_STATUS -eq 0 ]; then
    echo "✅ RDS Writer: CLEANED"
else
    echo "❌ RDS Writer: FAILED"
fi

if [ $CLOUDSQL_WRITER_STATUS -eq 0 ]; then
    echo "✅ Cloud SQL Writer: CLEANED"
else
    echo "❌ Cloud SQL Writer: FAILED"
fi

if [ $CLOUDSQL_READER_STATUS -eq 0 ]; then
    echo "✅ Cloud SQL Reader: CLEANED"
else
    echo "❌ Cloud SQL Reader: FAILED"
fi
echo ""

if [ $RDS_STATUS -eq 0 ] && [ $CLOUDSQL_WRITER_STATUS -eq 0 ] && [ $CLOUDSQL_READER_STATUS -eq 0 ]; then
    echo "🎉 All databases cleaned successfully!"
    echo ""
    echo "Next steps:"
    echo "  - Run test_setup.sql to recreate test tables"
    echo "  - Or start fresh with your own schema"
    exit 0
else
    echo "⚠️  Some databases failed to clean"
    exit 1
fi
