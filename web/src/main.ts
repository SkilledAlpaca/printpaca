import { mount } from "svelte";
import App from "./App.svelte";
import "./theme.css";

mount(App, { target: document.getElementById("app")! });

// App-shell service worker, admin SPA only. A silent no-op in dev (import.meta.env.PROD false) and on
// insecure-context LAN HTTP, where "serviceWorker" in navigator is false.
if (import.meta.env.PROD && "serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("sw.js").catch(() => {});
  });
}
