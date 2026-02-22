const path = require('path');

// Get the directory where this config file is located
const baseDir = __dirname;

module.exports = {
  apps: [{
    name: 'cf-dns-bot',
    script: path.join(baseDir, 'cf-dns-bot'),
    cwd: baseDir,
    instances: 1,
    autorestart: true,
    watch: false,
    max_memory_restart: '500M',
    min_uptime: '10s',
    max_restarts: 5,
    env: {
      NODE_ENV: 'production'
    },
    log_file: path.join(baseDir, 'logs', 'cf-dns-bot.log'),
    out_file: path.join(baseDir, 'logs', 'cf-dns-bot-out.log'),
    error_file: path.join(baseDir, 'logs', 'cf-dns-bot-error.log'),
    time: true,
    kill_timeout: 5000,
    listen_timeout: 10000
  }, {
    name: 'cf-dns-mcp',
    script: path.join(baseDir, 'cf-dns-mcp'),
    cwd: baseDir,
    instances: 1,
    autorestart: true,
    watch: false,
    max_memory_restart: '500M',
    min_uptime: '10s',
    max_restarts: 5,
    env: {
      NODE_ENV: 'production'
    },
    log_file: path.join(baseDir, 'logs', 'cf-dns-mcp.log'),
    out_file: path.join(baseDir, 'logs', 'cf-dns-mcp-out.log'),
    error_file: path.join(baseDir, 'logs', 'cf-dns-mcp-error.log'),
    time: true,
    kill_timeout: 5000,
    listen_timeout: 10000
  }]
};
