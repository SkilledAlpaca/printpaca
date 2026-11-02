import { mount } from "svelte";
import PublicApp from "./PublicApp.svelte";
import "../theme.css";

mount(PublicApp, { target: document.getElementById("app")! });
