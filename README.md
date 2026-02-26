# Cloudflare DNS Management Bot

A Telegram bot for managing Cloudflare DNS records with **button-based UI** and clean architecture.

## Features

- **🎛️ Button-Based UI**: No need to remember commands - just click buttons!
- **DNS Record CRUD**: Create, Read, Update, Delete DNS records
- **Record Types**: Supports A, AAAA, CNAME, MX, TXT, NS, SRV, CAA (Free tier)
- **Proxy Support**: Toggle Cloudflare proxy (orange cloud) for records
- **Access Request System**: Unauthorized users can request access, admin can approve/reject
- **MCP HTTP Server**: Built-in HTTP server for AI assistant integration with API key authentication
- **Clean Architecture**: Handler -> Usecase -> Repository pattern
- **Handler Agnostic**: Usecase can be used for Telegram Bot or REST API
- **Conversation State**: Multi-step form flow for creating records

## Architecture

```
cmd/bot/                    # Entry point
├── main.go

internal/
├── domain/                 # Entities (DNSRecord, Zone, errors)
│   ├── dns.go
│   ├── zone.go
│   └── errors.go
├── handler/                # Handler interfaces
│   ├── interfaces.go
│   └── telegram/           # Telegram implementation
│       ├── bot.go          # Button-based handlers
│       └── state.go        # Conversation state management
├── usecase/                # Business logic (handler-agnostic)
│   ├── interfaces.go
│   └── dns_usecase.go
└── repository/             # Repository interfaces & implementations
    ├── interfaces.go
    ├── dns_repository.go
    └── zone_repository.go

external_resource/          # External SDKs
└── cloudflare/
    ├── interfaces.go
    └── client.go

pkg/
├── config/                 # Configuration management
│   └── config.go
└── storage/                # JSON file storage
    ├── interfaces.go
    └── json_storage.go
```

## Prerequisites

- Go 1.21 or higher
- Cloudflare account with API token or API key
- Telegram bot token

## Installation

1. Clone the repository:
```bash
git clone <repository-url>
cd cf-dns-bot
```

2. Install dependencies:
```bash
go mod download
```

3. Copy environment file:
```bash
cp .env.example .env
```

4. Edit `.env` with your credentials:
```env
TELEGRAM_BOT_TOKEN=your_bot_token
TELEGRAM_ALLOWED_USERS=your_telegram_user_id
CLOUDFLARE_API_TOKEN=your_cf_api_token
```

## Configuration

### Telegram Bot Token

1. Message [@BotFather](https://t.me/botfather) on Telegram
2. Create a new bot with `/newbot`
3. Copy the token to `TELEGRAM_BOT_TOKEN`

### Cloudflare API Token

1. Go to [Cloudflare API Tokens](https://dash.cloudflare.com/profile/api-tokens)
2. Create a new token with these permissions:
   - Zone:Read
   - DNS:Edit
3. Copy the token to `CLOUDFLARE_API_TOKEN`

### Allowed Users

Set `TELEGRAM_ALLOWED_USERS` to comma-separated Telegram user IDs. Only these users can use the bot.

To get your user ID, message [@userinfobot](https://t.me/userinfobot) on Telegram.

## Usage

### Run the bot:

```bash
go run cmd/bot/main.go
```

### Or build and run:

```bash
go build -o cf-dns-bot cmd/bot/main.go
./cf-dns-bot
```

### Run with PM2 (Production)

```bash
# Build both services
go build -o cf-dns-bot ./cmd/bot
go build -o cf-dns-mcp ./cmd/mcp-server

# Start with PM2
pm2 start ecosystem.config.js

# View logs
pm2 logs

# Restart
pm2 restart cf-dns-bot
pm2 restart cf-dns-mcp
```

## How to Use

### Starting the Bot

1. Send `/start` to your bot on Telegram
2. You'll see the **Main Menu** with buttons:
   - 🔍 Manage Records
   - 🌐 MCP HTTP Server

### Access Request System

When an unauthorized user tries to use the bot:

1. User sees **⛔ Access Denied** with **📝 Request Access** button
2. User clicks the button to submit request
3. All admins receive **🔔 New Access Request** notification with user info
4. Admin can click **✅ Approve** or **❌ Reject**
5. User receives notification of the decision
6. If approved, user can immediately use the bot

### Managing Records

1. Click **🔍 Manage Records**
2. Select a **Zone** (domain)
3. You'll see all records with pagination (10 per page)
4. Click any record to view details with **✏️ Edit** and **🗑️ Delete** buttons
5. Or click **➕ Create** to add a new record in that zone

### Creating a DNS Record

**From Manage Records:**
1. Click **🔍 Manage Records** → Select Zone → **➕ Create**
2. Select **Record Type** (A, AAAA, CNAME, etc.)
3. Type the **Record Name** (e.g., `www`, `api`, `@` for root)
4. Type the **Content** (IP address, domain, etc.)
5. Select **TTL** from the buttons (Auto, 300, 600, etc.)
6. Select **Proxy** option (Yes/No)
7. Click **✅ Confirm Create**

### MCP HTTP Server Management

1. Click **🌐 MCP HTTP Server** from main menu
2. View server status and port
3. Use buttons to:
   - **▶️ Start Server** / **🛑 Stop Server** - Control the server
   - **🔢 Change Port** - Change the HTTP port (default: 8875)
   - **📊 Status** - View current status
   - **🔑 MCP API Keys** - Manage API keys for authentication

### Managing MCP API Keys

1. Go to **🌐 MCP HTTP Server** → **🔑 MCP API Keys**
2. **➕ Generate New Key** - Create a new API key (shown once)
3. **📋 List Keys** - View all keys (masked)
4. **🗑️ Delete Key** - Remove a key

## Button Interface

### Main Menu
```
┌───────────────────┐
│ 🔍 Manage Records │
├───────────────────┤
│ 🌐 MCP HTTP Server│
└───────────────────┘
```

### Manage Records Flow
```
Step 1: Select Zone
┌─────────┬─────────┐
│zone1.com│zone2.com│
├─────────┼─────────┤
│zone3.com│ Cancel  │
└─────────┴─────────┘

Step 2: View Records (with pagination)
🔍 Records in example.com:
Page 1/3 (25 records)

┌────────────┬────────────┐
│📄 www (A)  │📄 api (A)  │
├────────────┼────────────┤
│📄 blog(CNAM│📄 mail(MX) │
├────────────┼────────────┤
│⬅️ Prev     │📄 1/3      │
│Next ➡️     │            │
├────────────┼────────────┤
│🔄 Refresh  │➕ Create    │
├────────────┼────────────┤
│◀️ Back     │🏠 Menu      │
└────────────┴────────────┘

Step 3: Record Detail
📋 Record Details

Zone: example.com
Name: www
Type: A
Content: 192.168.1.1
TTL: 300
Proxied: Yes

┌──────────┬──────────┐
│✏️ Edit    │🗑️ Delete │
├──────────┼──────────┤
│◀️ Back    │🏠 Menu    │
└──────────┴──────────┘
```

### Create Record Flow
```
Step 1: Select Record Type
┌────┬──────┬───────┬────┐
│ A  │ AAAA │ CNAME │ MX │
├────┼──────┼───────┼────┤
│TXT │  NS  │  SRV  │CAA │
└────┴──────┴───────┴────┘

Step 5: Select TTL
┌─────────┬─────┬─────┐
│Auto (1) │ 300 │ 600 │
├─────────┼─────┼─────┤
│  1800   │3600 │86400│
└─────────┴─────┴─────┘

Step 6: Enable Proxy?
┌───────────────────┬───────────────────┐
│✅ Yes (Proxied)   │❌ No (DNS Only)   │
└───────────────────┴───────────────────┘
```

### MCP HTTP Server Menu
```
🌐 MCP HTTP Server Management

Status: 🟢 Running
Port: 8875

┌───────────────────┐
│🛑 Stop Server     │
├─────────┬─────────┤
│🔢 Change Port     │📊 Status│
├─────────┴─────────┤
│🔑 MCP API Keys    │
├───────────────────┤
│◀️ Back to Menu    │
└───────────────────┘
```

### MCP API Keys Menu
```
🔑 MCP API Key Management

┌───────────────────┐
│➕ Generate New Key│
├───────────────────┤
│📋 List Keys       │
├───────────────────┤
│🗑️ Delete Key      │
├───────────────────┤
│◀️ Back to MCP HTTP│
└───────────────────┘
```

### Access Request Flow
```
Unauthorized User:
┌──────────────────────────┐
│⛔ Access Denied          │
│                          │
│You are not authorized    │
│to use this bot.          │
├──────────────────────────┤
│📝 Request Access         │
└──────────────────────────┘

Admin Notification:
┌──────────────────────────┐
│🔔 New Access Request     │
│                          │
│User ID: 123456789        │
│Username: @johndoe        │
│Name: John Doe            │
├──────────────────────────┤
│✅ Approve  │  ❌ Reject  │
└──────────────────────────┘
```

## MCP Server

This project includes an MCP (Model Context Protocol) HTTP server that allows AI assistants like Claude to manage Cloudflare DNS records directly via HTTP API.

### What is MCP?

MCP (Model Context Protocol) is a protocol that enables AI assistants to interact with external tools and services. With MCP, you can ask Claude to manage your DNS records using natural language.

### MCP Server Features

- **HTTP Transport**: Uses HTTP instead of stdio for better flexibility
- **API Key Authentication**: Secure access with API key validation
- **Telegram Control**: Start/stop server and manage API keys via Telegram bot
- **Bundled with Bot**: MCP HTTP server runs alongside the Telegram bot

### MCP Server Tools

The MCP server provides 7 tools:

| Tool | Description |
|------|-------------|
| `list_zones` | List all Cloudflare zones (domains) |
| `list_records` | List DNS records for a specific zone |
| `get_record` | Get details of a specific record |
| `create_record` | Create a new DNS record |
| `update_record` | Update an existing DNS record |
| `delete_record` | Delete a DNS record |
| `upsert_record` | Create or update a record (idempotent) |

### Running the MCP Server

The MCP HTTP server is **bundled with the Telegram bot** and starts automatically. You can control it via Telegram:

1. Send `/start` to your bot
2. Click **🌐 MCP HTTP Server**
3. Use buttons to start/stop the server

Or use PM2 to run both services:

```bash
# Build both services
go build -o cf-dns-bot ./cmd/bot

# Start with PM2
pm2 start ecosystem.config.js

# View logs
pm2 logs cf-dns-bot
```

### Configuring Claude Desktop

To use the MCP HTTP server with Claude Desktop, add this to your `claude_desktop_config.json`:

**macOS:**
```json
{
  "mcpServers": {
    "cf-dns": {
      "url": "http://localhost:8875",
      "env": {
        "MCP_API_KEY": "your_api_key_from_telegram"
      }
    }
  }
}
```

**Windows:**
```json
{
  "mcpServers": {
    "cf-dns": {
      "url": "http://localhost:8875",
      "env": {
        "MCP_API_KEY": "your_api_key_from_telegram"
      }
    }
  }
}
```

**Config file location:**
- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%/Claude/claude_desktop_config.json`
- Linux: `~/.config/Claude/claude_desktop_config.json`

**Getting API Key:**
1. Open Telegram bot
2. Go to **🌐 MCP HTTP Server** → **🔑 MCP API Keys**
3. Click **➕ Generate New Key**
4. Copy the key to your config

### Using MCP with Claude

Once configured, you can ask Claude to manage your DNS:

```
"List all my Cloudflare zones"
"Show me DNS records for example.com"
"Create an A record for www.example.com pointing to 192.168.1.1"
"Update the TTL of api.example.com to 600"
"Delete the test.example.com record"
```

### Example MCP HTTP API Usage

```bash
# Generate API key from Telegram bot first
# Then use it in requests

# List all zones
curl -X POST http://localhost:8875 \
  -H "Authorization: Bearer your_api_key" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_zones","arguments":{}}}'

# List records for a zone
curl -X POST http://localhost:8875 \
  -H "Authorization: Bearer your_api_key" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_records","arguments":{"zone_name":"example.com"}}}'

# Create a record
curl -X POST http://localhost:8875 \
  -H "Authorization: Bearer your_api_key" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_record","arguments":{"zone_name":"example.com","name":"www","type":"A","content":"192.168.1.1","ttl":300,"proxied":true}}}'
```

## Supported Record Types

| Type | Description | Example Content |
|------|-------------|-----------------|
| A | IPv4 address | `192.168.1.1` |
| AAAA | IPv6 address | `2001:db8::1` |
| CNAME | Canonical name | `example.github.io` |
| MX | Mail exchange | `mail.example.com` |
| TXT | Text record | `v=spf1 include:_spf.google.com ~all` |
| NS | Nameserver | `ns1.example.com` |
| SRV | Service locator | `10 5 5060 sipserver.example.com` |
| CAA | Certificate Authority | `0 issue "letsencrypt.org"` |

## Tutorial: Step-by-Step Guide

### Tutorial 1: Initial Setup

1. **Get Cloudflare API Token**
   ```
   1. Login to https://dash.cloudflare.com
   2. Go to My Profile → API Tokens
   3. Click "Create Token"
   4. Use "Edit zone DNS" template
   5. Select your zone (domain)
   6. Copy the token
   ```

2. **Get Telegram Bot Token**
   ```
   1. Open Telegram and search @BotFather
   2. Send /newbot
   3. Follow instructions to create bot
   4. Copy the bot token
   ```

3. **Get Your Telegram User ID**
   ```
   1. Search @userinfobot on Telegram
   2. Start the bot
   3. Copy your user ID
   ```

4. **Configure Environment**
   ```bash
   cp .env.example .env
   # Edit .env and fill in:
   # TELEGRAM_BOT_TOKEN=your_bot_token
   # TELEGRAM_ALLOWED_USERS=your_user_id
   # CLOUDFLARE_API_TOKEN=your_cf_token
   ```

### Tutorial 2: Create Your First DNS Record

1. **Start the bot**
   ```bash
   go run cmd/bot/main.go
   ```

2. **In Telegram:**
   ```
   1. Send /start to your bot
   2. Click "🔍 Manage Records"
   3. Select your zone (domain)
   4. Click "➕ Create"
   5. Select "A" record type
   6. Type: www
   7. Type: 192.168.1.1 (your server IP)
   8. Select TTL: 300
   9. Select: ✅ Yes (Proxied)
   10. Click: ✅ Confirm Create
   ```

3. **Verify the record**
   ```bash
   nslookup www.yourdomain.com
   # Should show Cloudflare IPs (if proxied)
   ```

### Tutorial 3: Manage Existing Records

1. **List all records**
   ```
   1. Send /start
   2. Click "🔍 Manage Records"
   3. Select your zone
   4. Browse records with Prev/Next buttons (10 per page)
   ```

2. **Edit a record**
   ```
   1. Click on any record button (📄 record name)
   2. Click "✏️ Edit"
   3. Choose what to edit (Content, TTL, or Proxy)
   4. Enter new value
   ```

3. **Delete a record**
   ```
   1. Click on record
   2. Click "🗑️ Delete"
   3. Confirm deletion
   ```

4. **Create new record in zone**
   ```
   1. In Manage Records view, click "➕ Create"
   2. Follow the create flow (no need to select zone again)
   ```

### Tutorial 4: Using MCP HTTP Server with Claude

1. **Start the bot** (MCP HTTP server starts automatically)
   ```bash
   go run cmd/bot/main.go
   ```

2. **Generate API Key via Telegram**
   ```
   1. Send /start to your bot
   2. Click "🌐 MCP HTTP Server"
   3. Click "🔑 MCP API Keys"
   4. Click "➕ Generate New Key"
   5. Copy the key (shown only once!)
   ```

3. **Configure Claude Desktop**
   ```json
   {
     "mcpServers": {
       "cf-dns": {
         "url": "http://localhost:8875",
         "env": {
           "MCP_API_KEY": "your_api_key_here"
         }
       }
     }
   }
   ```

4. **Ask Claude to manage DNS**
   ```
   "List all my zones"
   "Create A record blog.example.com pointing to 192.168.1.1"
   "Delete test.example.com"
   ```

### Tutorial 5: Granting Access to New Users

1. **User Requests Access**
   ```
   1. Unauthorized user sends /start to bot
   2. Sees "⛔ Access Denied" with "📝 Request Access" button
   3. Clicks button to submit request
   ```

2. **Admin Approves Access**
   ```
   1. All admins receive "🔔 New Access Request" notification
   2. Admin sees user info (ID, username, name)
   3. Admin clicks "✅ Approve"
   4. User is immediately granted access
   ```

3. **User Gets Access**
   ```
   1. User receives "🎉 Access Granted!" notification
   2. User can now send /start and use the bot
   ```

### Tutorial 6: Production Deployment

1. **Build binary**
   ```bash
   go build -o cf-dns-bot ./cmd/bot
   ```

2. **Install PM2**
   ```bash
   npm install -g pm2
   ```

3. **Start services**
   ```bash
   pm2 start ecosystem.config.js
   pm2 save
   pm2 startup
   ```

4. **Monitor logs**
   ```bash
   pm2 logs
   pm2 logs cf-dns-bot
   ```

5. **Auto-restart on boot**
   ```bash
   pm2 save
   pm2 startup systemd
   # Follow the command output
   ```

## Project Structure for REST API (Future)

The usecase layer is handler-agnostic. To add a REST API handler:

1. Create `cmd/api/main.go`
2. Create `internal/handler/http/` package
3. Use the same `usecase.DNSUsecase` interface

Example:
```go
// internal/handler/http/server.go
func (s *Server) handleCreateRecord(c *gin.Context) {
    var input usecase.CreateRecordInput
    if err := c.BindJSON(&input); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    record, err := s.dnsUsecase.CreateRecord(c.Request.Context(), input)
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }

    c.JSON(200, record)
}
```

## Data Storage

The bot uses JSON file storage for configuration (no database required):

```json
// data/config.json
{
  "allowed_users": [123456789],
  "pending_requests": [
    {
      "user_id": 987654321,
      "username": "johndoe",
      "first_name": "John",
      "last_name": "Doe"
    }
  ],
  "default_ttl": 300,
  "default_proxied": true,
  "mcp_api_keys": ["api_key_here"],
  "mcp_http_port": "8875",
  "mcp_http_enabled": true
}
```

## License

MIT
