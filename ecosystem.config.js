module.exports = {
  apps: [{
    name: 'cf-dns-bot',
    script: './cf-dns-bot',
    cwd: '/home/rhyanz46/cf-dns-bot',
    instances: 1,
    autorestart: true,
    watch: false,
    max_memory_restart: '500M',
    min_uptime: '10s',
    max_restarts: 5,
    env: {
      NODE_ENV: 'production'
    },
    log_file: '/tmp/cf-dns-bot.log',
    out_file: '/tmp/cf-dns-bot-out.log',
    error_file: '/tmp/cf-dns-bot-error.log',
    time: true,
    kill_timeout: 5000,
    listen_timeout: 10000
  }, {
    name: 'cf-dns-mcp',
    script: './cf-dns-mcp',
    cwd: '/home/rhyanz46/cf-dns-bot',
    instances: 1,
    autorestart: true,
    watch: false,
    max_memory_restart: '500M',
    min_uptime: '10s',
    max_restarts: 5,
    env: {
      NODE_ENV: 'production'
    },
    log_file: '/tmp/cf-dns-mcp.log',
    out_file: '/tmp/cf-dns-mcp-out.log',
    error_file: '/tmp/cf-dns-mcp-error.log',
    time: true,
    kill_timeout: 5000,
    listen_timeout: 10000
  }]
};
