import "./index.css";
import "./utils/clipboardPolyfill";
import { render } from "preact";
import { App } from "./app";

render(<App />, document.getElementById("app")!);
