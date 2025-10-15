#!/bin/bash

# Script to run setup.sql through pprox
# Creates the users table and inserts initial test data
#
# **Run this on your LOCAL MACHINE**
# It connects to pprox running on the PPROX VM

# Load environment variables
source ~/pprox-setup.env

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
SQL_FILE="$SCRIPT_DIR/setup.sql"

if [ ! -f "$SQL_FILE" ]; then
    echo "❌ Error: setup.sql not found at $SQL_FILE"
    exit 1
fi

echo "=========================================="
echo "Running Database Setup via pprox"
echo "=========================================="
echo ""
echo "This will create the users table and insert 5 test rows"
echo "through pprox proxy server ($PPROX_VM_IP:54329)"
echo ""

# Run setup through pprox
echo "Setting up databases through pprox..."
echo "   Proxy: $PPROX_VM_IP:54329"
echo "------------------------------------------"
export PGPASSWORD="$APP_USER_PASSWORD"
export PGSSLMODE=require
psql -h $PPROX_VM_IP -p 54329 -U app_user -d postgres -f "$SQL_FILE"
STATUS=$?
echo ""

# Summary
echo "=========================================="
echo "Setup Summary"
echo "=========================================="
if [ $STATUS -eq 0 ]; then
    echo "✅ Database setup: SUCCESS"
    echo ""
    echo "🎉 Setup completed successfully!"
    echo ""
    echo "Note: pprox automatically routes:"
    echo "  - Writes to: RDS Writer + Cloud SQL Writer"
    echo "  - Reads from: Cloud SQL Reader"
    echo ""
    echo "Next steps:"
    echo "  - Run ./tests/view.sh to view data"
    echo "  - Run ./tests/run_addrows.sh to add more test data"
    exit 0
else
    echo "❌ Database setup: FAILED"
    echo ""
    echo "⚠️  Setup failed"
    echo ""
    echo "Troubleshooting:"
    echo "  - Check if pprox is running: systemctl status pprox"
    echo "  - Check pprox logs: journalctl -u pprox -f"
    exit 1
fi
