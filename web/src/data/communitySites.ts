export interface CommunitySite {
  id: string;
  nameKey: string;
  descKey: string;
  serverURL: string;
  gatewayURL: string;
  serverAliases?: string[];
  gatewayAliases?: string[];
}

/** Communities whose user service accepts CSGLite login (`state=lite`) and
 * whose AI Gateway is reachable. Hong Kong OCC and Whale Intelligence are
 * omitted: their token APIs do not match this client. */
export const COMMUNITY_SITES: CommunitySite[] = [
  {
    id: "main",
    nameKey: "settings.siteMain",
    descKey: "settings.siteMainDesc",
    serverURL: "https://hub.opencsg.com",
    gatewayURL: "https://ai.space.opencsg.com",
    serverAliases: ["https://opencsg.com"],
    gatewayAliases: ["https://opencsg.com/aigateway"],
  },
  {
    id: "sx",
    nameKey: "settings.siteSx",
    descKey: "settings.siteSxDesc",
    serverURL: "https://sanxia.opencsg.com",
    gatewayURL: "https://sanxia.opencsg.com/aigateway",
  },
  {
    id: "lg",
    nameKey: "settings.siteLg",
    descKey: "settings.siteLgDesc",
    serverURL: "https://modelhub.lgdg.cc",
    gatewayURL: "https://modelhub.lgdg.cc/aigateway",
  },
  {
    id: "df",
    nameKey: "settings.siteDf",
    descKey: "settings.siteDfDesc",
    serverURL: "https://openeast.opencsg.com",
    gatewayURL: "https://openeast.opencsg.com/aigateway",
  },
  {
    id: "csg",
    nameKey: "settings.siteCsg",
    descKey: "settings.siteCsgDesc",
    serverURL: "https://opencsg.io",
    gatewayURL: "https://opencsg.io/aigateway",
  },
];

export const CUSTOM_SITE_ID = "custom";

export function normalizeServiceURL(value: string): string {
  return value.trim().replace(/\/+$/, "").toLowerCase();
}

export function matchCommunitySite(
  serverURL: string,
  gatewayURL: string,
  defaults: { serverURL?: string; gatewayURL?: string } = {},
): string {
  const server = normalizeServiceURL(serverURL);
  const gateway = normalizeServiceURL(gatewayURL);
  for (const site of COMMUNITY_SITES) {
    const servers = new Set([site.serverURL, ...(site.serverAliases ?? [])].map(normalizeServiceURL));
    const gateways = new Set([site.gatewayURL, ...(site.gatewayAliases ?? [])].map(normalizeServiceURL));
    if (site.id === "main") {
      const defaultServer = normalizeServiceURL(defaults.serverURL || site.serverURL);
      const defaultGateway = normalizeServiceURL(defaults.gatewayURL || site.gatewayURL);
      if (defaultServer) servers.add(defaultServer);
      if (defaultGateway) gateways.add(defaultGateway);
    }
    if (servers.has(server) && gateways.has(gateway)) return site.id;
  }
  return CUSTOM_SITE_ID;
}
