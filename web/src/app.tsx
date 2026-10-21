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
import { Cluster } from "./pages/Cluster";
import { Observability, ObservabilityTraceDetailPage } from "./pages/Observability";
import { Team } from "./pages/Team";
import { Login } from "./pages/Login";
import { Invite } from "./pages/Invite";
import { AccountKeys } from "./pages/AccountKeys";
import { ChangePasswordDialog } from "./components/ChangePasswordDialog";
import { useEffect } from "preact/hooks";
import { authChecked, authSession, installAuthInterceptor, loadAuthSession, loginRequired, mustChangePassword } from "./auth";

installAuthInterceptor();

export function App() {
  return (
    <LocationProvider>
      <AppRoutes />
    </LocationProvider>
  );
}

function AppRoutes() {
  const { path } = useLocation();

  useEffect(() => {
    void loadAuthSession();
  }, []);

  // An invitation link works whether or not anyone is signed in.
  if (path.startsWith("/invite/")) {
    return <Invite token={decodeURIComponent(path.slice("/invite/".length))} />;
  }
  // Wait for the first session check so a signed-out user never sees pages
  // flash and fail before the sign-in form appears.
  if (!authChecked.value) return null;
  if (loginRequired.value) return <Login />;
  if (mustChangePassword(authSession.value)) return <ChangePasswordDialog forced />;

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
        <Route path="/cluster" component={Cluster} />
        <Route path="/observability/traces/:traceID" component={ObservabilityTraceDetailPage} />
        <Route path="/observability" component={Observability} />
        <Route path="/team" component={Team} />
        <Route path="/account/keys" component={AccountKeys} />
        <Route path="/settings" component={Settings} />
        <Route path="/pricing" component={Pricing} />
      </Router>
    </Layout>
  );
}
