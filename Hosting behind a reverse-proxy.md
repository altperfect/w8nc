To host w8nc behind a VPS like nginx, it is recommended to use a dedicated subdomain rather than a specific path like `/w8nc`.

## nginx configuration

1. In `/etc/nginx/sites-available`, create a configuration file called `w8nc.conf`. You can use the configuration below:

```
upstream w8nc_app {
	server 127.0.0.1:8080;
}

server {
	listen 443 ssl http2;
    # change the line below to your desired domain
	server_name w8nc.your-domain.com;

    # ssl configuration
	ssl_certificate /etc/letsencrypt/live/your-domain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/your-domain.com/privkey.pem;
    ssl_trusted_certificate /etc/letsencrypt/live/your-domain.com/chain.pem;

	location / {
		proxy_pass http://w8nc_app;

		proxy_http_version 1.1;
		proxy_set_header Host $host;
		proxy_set_header X-Real-IP $remote_addr;
		proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
		proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

2. Activate the configuration:

```bash
sudo ln -s /etc/nginx/sites-available/w8nc.conf /etc/nginx/sites-enabled/w8nc.conf
```

3. Validate nginx configuration and restart the service:

```bash
sudo nginx -t && sudo systemctl restart nginx
```

## w8nc configuration

1. In `docker-compose.yml`, set `COOKIE_SECURE` value to `true`.

2. Optionally, adjust other configuration values, like max response size or timeouts.

3. Deploy the app:

```bash
make deploy
```

If everything worked, you should see the following healthcheck results:

```json
{"database":"ok","notify_binary":"ok","status":"ok"}
```

## changing the app's port

By default, the app uses port `8080` for functioning. In case it's busy on your system, you'd have to edit a few files in the app's directory. In the example below, we will use the value `9099` as a new port.

- For `docker-compose.yml`, in the `ports` section, change `- "127.0.0.1:8080:8080"` to `- "127.0.0.1:9099:8080"`;
- For `Makefile`, change `APP_URL ?= http://127.0.0.1:8080` to `APP_URL ?= http://127.0.0.1:9099`;
- For `w8nc.conf` (nginx), change `server 127.0.0.1:8080;` to `server 127.0.0.1:9099;`.

Then, run `make deploy` again.

