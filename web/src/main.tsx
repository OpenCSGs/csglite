import "./index.css";
import "./utils/clipboardPolyfill";
import "./utils/publicSession";
import { render } from "preact";
import { App } from "./app";

render(<App />, document.getElementById("app")!);
