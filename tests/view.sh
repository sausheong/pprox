#!/bin/bash

# Test script to connect directly to all three databases
# and query the users table
#
# **IMPORTANT: This script must be run on the PPROX VM**
# The databases are only accessible from the PPROX VM IP due to firewall rules
#
# To run this script:
# 1. Transfer to VM: gcloud compute scp test_direct_connections.sh pprox-server:~/ --zone=$GCP_ZONE
# 2. SSH to VM: gcloud compute ssh pprox-server --zone=$GCP_ZONE
# 3. Run: chmod +x test_direct_connections.sh && ./test_direct_connections.sh

# Load environment variables
source ~/pprox-setup.env

echo "=========================================="
echo "Testing Direct Database Connections"
echo "=========================================="
echo ""

# Test RDS Writer
echo "1. Testing RDS Writer (pprox-writer-rds)"
echo "   Host: $RDS_ENDPOINT"
echo "------------------------------------------"
export PGPASSWORD="$APP_USER_PASSWORD"
export PGCONNECT_TIMEOUT=5
timeout 5 psql -h "$RDS_ENDPOINT" -p 5432 -U app_user -d postgres -c "SELECT * FROM users ORDER BY id;" 2>&1
RDS_STATUS=$?
if [ $RDS_STATUS -eq 124 ]; then
    echo "⏱️  Connection timed out after 5 seconds"
fi
echo ""

# Test Cloud SQL Writer (Primary)
echo "2. Testing Cloud SQL Writer (pprox-writer-primary)"
echo "   Host: $CLOUDSQL_WRITER_HOST"
echo "------------------------------------------"
export PGSSLMODE=require
export PGPASSWORD="$APP_USER_PASSWORD"
export PGCONNECT_TIMEOUT=5
timeout 5 psql -h "$CLOUDSQL_WRITER_HOST" -p 5432 -U app_user -d postgres -c "SELECT * FROM users ORDER BY id;" 2>&1
CLOUDSQL_WRITER_STATUS=$?
if [ $CLOUDSQL_WRITER_STATUS -eq 124 ]; then
    echo "⏱️  Connection timed out after 5 seconds"
fi
echo ""

# Test Cloud SQL Reader (Replica)
echo "3. Testing Cloud SQL Reader (pprox-reader-replica)"
echo "   Host: $CLOUDSQL_READER_HOST"
echo "------------------------------------------"
export PGSSLMODE=require
export PGPASSWORD="$APP_USER_PASSWORD"
export PGCONNECT_TIMEOUT=5
timeout 5 psql -h "$CLOUDSQL_READER_HOST" -p 5432 -U app_user -d postgres -c "SELECT * FROM users ORDER BY id;" 2>&1
CLOUDSQL_READER_STATUS=$?
if [ $CLOUDSQL_READER_STATUS -eq 124 ]; then
    echo "⏱️  Connection timed out after 5 seconds"
fi
echo ""

# Summary
echo "=========================================="
echo "Connection Test Summary"
echo "=========================================="
if [ $RDS_STATUS -eq 0 ]; then
    echo "✅ RDS Writer: SUCCESS"
else
    echo "❌ RDS Writer: FAILED"
fi

if [ $CLOUDSQL_WRITER_STATUS -eq 0 ]; then
    echo "✅ Cloud SQL Writer: SUCCESS"
else
    echo "❌ Cloud SQL Writer: FAILED"
fi

if [ $CLOUDSQL_READER_STATUS -eq 0 ]; then
    echo "✅ Cloud SQL Reader: SUCCESS"
else
    echo "❌ Cloud SQL Reader: FAILED"
fi
echo ""
