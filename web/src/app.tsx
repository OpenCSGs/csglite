import { LocationProvider, Router, Route, useLocation } from "preact-iso";
import { Layout } from "./components/Layout";
import { Dashboard } from "./pages/Dashboard";
import { Marketplace } from "./pages/Marketplace";
import { Library } from "./pages/Library";
import { LibraryModelDetail } from "./pages/LibraryModelDetail";
import { Datasets } from "./pages/Datasets";
import { Chat } from "./pages/Chat";
import { ImageGeneration } from "./pages/ImageGeneration";
import { ImageHistory } from "./pages/ImageHistory";
import { Settings } from "./pages/Settings";
import { Pricing } from "./pages/Pricing";
import { AIApps } from "./pages/AIApps";
import { AIAppShell } from "./pages/AIAppShell";
import { AIGateway } from "./pages/AIGateway";

export function App() {
  return (
    <LocationProvider>
      <AppRoutes />
    </LocationProvider>
  );
}

function AppRoutes() {
  const { path } = useLocation();

  if (path === "/ai-apps/shell") {
    return <AIAppShell />;
  }

  return (
    <Layout>
      <Router>
        <Route path="/" component={Dashboard} />
        <Route path="/marketplace" component={Marketplace} />
        <Route path="/library/detail/:model" component={LibraryModelDetail} />
        <Route path="/library" component={Library} />
        <Route path="/datasets" component={Datasets} />
        <Route path="/chat" component={Chat} />
        <Route path="/images/history" component={ImageHistory} />
        <Route path="/images" component={ImageGeneration} />
        <Route path="/ai-apps" component={AIApps} />
        <Route path="/ai-gateway" component={AIGateway} />
        <Route path="/settings" component={Settings} />
        <Route path="/pricing" component={Pricing} />
      </Router>
    </Layout>
  );
}
