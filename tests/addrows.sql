-- Add more rows to users table after primary failover
-- This script can be used to test that writes continue working after failover

INSERT INTO users (username, email, full_name, is_active) VALUES
    ('frank', 'frank@example.com', 'Frank Wilson', true),
    ('grace', 'grace@example.com', 'Grace Lee', true),
    ('henry', 'henry@example.com', 'Henry Chen', false),
    ('iris', 'iris@example.com', 'Iris Taylor', true),
    ('jack', 'jack@example.com', 'Jack Anderson', true),
    ('kate', 'kate@example.com', 'Kate Thompson', true),
    ('leo', 'leo@example.com', 'Leo Garcia', false),
    ('maria', 'maria@example.com', 'Maria Rodriguez', true),
    ('nathan', 'nathan@example.com', 'Nathan White', true),
    ('olivia', 'olivia@example.com', 'Olivia Harris', true);

-- Display all users to verify the insert
SELECT COUNT(*) as total_users FROM users;
SELECT * FROM users ORDER BY id;
