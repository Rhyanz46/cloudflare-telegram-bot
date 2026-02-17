# Cloudflare DNS Management Bot

A Telegram bot for managing Cloudflare DNS records with **button-based UI** and clean architecture.

## Features

- **🎛️ Button-Based UI**: No need to remember commands - just click buttons!
- **Zone Management**: List all your Cloudflare zones (domains)
- **DNS Record CRUD**: Create, Read, Update, Delete DNS records
- **Record Types**: Supports A, AAAA, CNAME, MX, TXT, NS, SRV, CAA (Free tier)
- **Proxy Support**: Toggle Cloudflare proxy (orange cloud) for records
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

## How to Use

### Starting the Bot

1. Send `/start` to your bot on Telegram
2. You'll see the **Main Menu** with buttons:
   - 📋 List Zones
   - ➕ Create Record
   - 🔍 Manage Records

### Creating a DNS Record (Button Flow)

1. Click **➕ Create Record**
2. Select a **Zone** (domain) from the buttons
3. Select **Record Type** (A, AAAA, CNAME, etc.)
4. Type the **Record Name** (e.g., `www`, `api`, `@` for root)
5. Type the **Content** (IP address, domain, etc.)
6. Select **TTL** from the buttons (Auto, 300, 600, etc.)
7. Select **Proxy** option (Yes/No)
8. Click **✅ Confirm Create**

### Managing Records

1. Click **🔍 Manage Records**
2. Select a **Zone**
3. You'll see all records with **✏️ Edit** and **🗑️ Delete** buttons
4. Click the action button for the record you want to modify

### Listing Zones

1. Click **📋 List Zones**
2. All your zones will be displayed

## Button Interface

### Main Menu
```
┌─────────────────┐
│  📋 List Zones  │
├─────────────────┤
│ ➕ Create Record│
├─────────────────┤
│ 🔍 Manage Records│
└─────────────────┘
```

### Create Record Flow
```
Step 1: Select Zone
┌─────────┬─────────┐
│zone1.com│zone2.com│
├─────────┼─────────┤
│zone3.com│ Cancel  │
└─────────┴─────────┘

Step 2: Select Type
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

### Manage Records View
```
🔍 Records in example.com:

• www A → 192.168.1.1 🟠
┌──────────┬──────────┐
│✏️ www    │🗑️ www    │
├──────────┼──────────┤
│✏️ api    │🗑️ api    │
├──────────┼──────────┤
│✏️ blog   │🗑️ blog   │
└──────────┴──────────┘
┌─────────┬───────────┐
│🔄 Refresh│➕ Create  │
├─────────┼───────────┤
│◀️ Back   │🏠 Menu    │
└─────────┴───────────┘
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
  "default_ttl": 300,
  "default_proxied": true
}
```

## License

MIT
