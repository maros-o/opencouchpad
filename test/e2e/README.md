# End-to-end browser tests

These drive the real web app in headless Chromium with synthetic multi-touch (CDP) and check the
exact messages sent to the server. They use the dry-run driver, so nothing moves on your machine.

```sh
go build -o /tmp/openremote ./cmd/openremote
/tmp/openremote --driver dryrun --port 8765 --host e2e-a --name "Test Box" &
/tmp/openremote --driver dryrun --port 8766 --host e2e-b --name "PIN Box" --pin 4321 &
cd test/e2e && npm install && npx playwright-core install chromium   # or set CHROME_PATH
npm run gestures   # prints per-gesture results and saves screenshots
npm run pin        # PIN flow + device switcher
```
