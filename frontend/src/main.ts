import { mount } from "svelte";
import App from "./App.svelte";
import "@kenn-io/kit-ui/theme.css";
import "@kenn-io/kit-ui/mermaid.css";
import "./app.css";
import { initI18n } from "./lib/i18n/index.js";
import { installPerfFetchInstrumentation } from "./lib/stores/perf.svelte.js";
// kit-ui-check-ignore: primes runed's debounce for layerchart; not a local debounce implementation
import { primeChartDebounce } from "./lib/utils/chart-debounce.js";

const target = document.getElementById("app");

if (!target) {
  throw new Error("Root element 'app' not found. Cannot mount application.");
}

installPerfFetchInstrumentation();
initI18n();
// Must run before any chart mounts; see chart-debounce.ts.
primeChartDebounce();

mount(App, { target });
