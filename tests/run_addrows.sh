#!/bin/bash

# Script to run addrows.sql on all databases
# Adds 10 more rows to the users table
#
# **IMPORTANT: This script must be run on the PPROX VM**
# The databases are only accessible from the PPROX VM IP due to firewall rules
#
# To run this script:
# 1. Transfer to VM: gcloud compute scp tests/run_addrows.sh pprox-server:~/tests/ --zone=$GCP_ZONE
# 2. SSH to VM: gcloud compute ssh pprox-server --zone=$GCP_ZONE
# 3. Run: chmod +x tests/run_addrows.sh && ./tests/run_addrows.sh

# Load environment variables
source ~/pprox-setup.env

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
SQL_FILE="$SCRIPT_DIR/addrows.sql"

if [ ! -f "$SQL_FILE" ]; then
    echo "❌ Error: addrows.sql not found at $SQL_FILE"
    exit 1
fi

echo "=========================================="
echo "Adding More Test Data via pprox"
echo "=========================================="
echo ""
echo "This will add 10 more rows to the users table"
echo "through pprox proxy server (localhost:54329)"
echo ""

# Add rows through pprox
echo "Adding rows through pprox..."
echo "   Proxy: localhost:54329"
echo "------------------------------------------"
export PGPASSWORD="$APP_USER_PASSWORD"
export PGSSLMODE=require
psql -h localhost -p 54329 -U app_user -d postgres -f "$SQL_FILE"
STATUS=$?
echo ""

# Summary
echo "=========================================="
echo "Add Rows Summary"
echo "=========================================="
if [ $STATUS -eq 0 ]; then
    echo "✅ Add rows: SUCCESS"
    echo ""
    echo "🎉 Rows added successfully!"
    echo ""
    echo "Note: pprox automatically routes:"
    echo "  - Writes to: RDS Writer + Cloud SQL Writer"
    echo "  - Reads from: Cloud SQL Reader"
    echo ""
    echo "Next steps:"
    echo "  - Run ./tests/view.sh to view updated data"
    exit 0
else
    echo "❌ Add rows: FAILED"
    echo ""
    echo "⚠️  Adding rows failed"
    echo ""
    echo "Troubleshooting:"
    echo "  - Check if pprox is running: systemctl status pprox"
    echo "  - Check pprox logs: journalctl -u pprox -f"
    exit 1
fi
