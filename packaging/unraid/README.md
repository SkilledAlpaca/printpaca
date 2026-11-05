# printpaca on Unraid

This directory holds the dockerman and Community Applications (CA) template for running printpaca on Unraid: `printpaca.xml` and its icon, `printpaca.png`.

## Install route 1: paste the template URL

You don't need a CA listing to run printpaca on Unraid.

1. Open the **Docker** tab in the Unraid web UI and click **Add Container**.
2. Switch the template dropdown to advanced view, paste this URL into **Template URL** (or **Repository**, depending on your Unraid version), and press Tab or click elsewhere to load it:

   ```
   https://raw.githubusercontent.com/SkilledAlpaca/printpaca/main/packaging/unraid/printpaca.xml
   ```

3. Set **ADMIN_PASSWORD**. The template requires it, because printpaca's default, a random password written to the container log, is often missed on Unraid.
4. Before you click **Apply**, note the appdata path for **Config** (`/mnt/user/appdata/printpaca` by default). Unraid creates the directory as root when you add the container. The image runs as UID/GID 65532 (the Dockerfile is the source of truth) and has no shell to fix ownership, so run this in a terminal on the Unraid host:

   ```
   chown 65532:65532 /mnt/user/appdata/printpaca
   ```

   Without it, the container refuses to run against a directory it can't write to and exits on every start.

5. Click **Apply**. Once the container starts, the WebUI link on the Docker tab (also `http://<unraid-ip>:8080/admin/`) opens the admin UI. Sign in with the password you set in step 3.

The WebUI link follows the container's port mapping, so it updates if you remap the host ports.

## Install route 2: get listed in Community Applications

This route is for the maintainer publishing printpaca to the CA app store, so it appears in Unraid's **Apps** tab search.

1. **Use this repository as the template feed.** CA indexes public GitHub repositories, and `https://github.com/SkilledAlpaca/printpaca` serves `packaging/unraid/printpaca.xml` from a raw GitHub URL.
2. **Keep the icon URL stable.** The template's `<Icon>` points at `https://raw.githubusercontent.com/SkilledAlpaca/printpaca/main/packaging/unraid/printpaca.png`. CA caches templates and icons, so don't move the file once published.
3. **Confirm the support link resolves.** CA requires a working support link; the template's `<Support>` field points at `https://github.com/SkilledAlpaca/printpaca/issues`.
4. **Post to the CA thread on the Unraid forums.** CA has no submission form or pull request flow. Reply in the [Community Applications feed thread](https://forums.unraid.net/topic/38582-plug-in-community-applications/) asking for your template feed to be added, and link the repository. A CA maintainer reviews the template and adds the feed; there's no fixed turnaround. Ready-to-paste text:

   ```
   Requesting my template feed be added to Community Applications.

   App: printpaca, a self-hosted 3D-print cost and quote calculator
   Template feed: https://github.com/SkilledAlpaca/printpaca
   Template file: printpaca.xml
   Support: https://github.com/SkilledAlpaca/printpaca/issues
   Project: https://github.com/SkilledAlpaca/printpaca

   Thanks!
   ```

5. After the feed is added, users find printpaca by searching **Apps**, and install it the same way as route 1 without pasting a URL.

## Console access doesn't work

The image is built `FROM scratch`: it contains the printpaca binary, a CA bundle, `THIRD_PARTY_NOTICES.md`, and an empty `/config` directory, nothing else. Unraid's **Console** button on the Docker tab opens a shell in the container, and this image has none, so the button fails or shows an empty terminal. Use `docker logs printpaca` or the Docker tab's log viewer to check server output.
