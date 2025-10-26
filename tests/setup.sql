-- Create users table
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    email VARCHAR(100) NOT NULL UNIQUE,
    full_name VARCHAR(100),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    is_active BOOLEAN DEFAULT true
);

-- Insert sample data
INSERT INTO users (username, email, full_name, is_active) VALUES
    ('alice', 'alice@example.com', 'Alice Johnson', true),
    ('bob', 'bob@example.com', 'Bob Smith', true),
    ('charlie', 'charlie@example.com', 'Charlie Brown', false),
    ('diana', 'diana@example.com', 'Diana Prince', true),
    ('eve', 'eve@example.com', 'Eve Martinez', true);
