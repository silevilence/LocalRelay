export namespace main {
	
	export class AppInfo {
	    version: string;
	    releaseRepo: string;
	    installScope: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.releaseRepo = source["releaseRepo"];
	        this.installScope = source["installScope"];
	    }
	}
	export class ProviderModel {
	    id: string;
	    name: string;

	    static createFrom(source: any = {}) {
	        return new ProviderModel(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class ProviderTestResult {
	    model: string;
	    content: string;
	    latencyMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ProviderTestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.content = source["content"];
	        this.latencyMs = source["latencyMs"];
	    }
	}
	export class UpdateInfo {
	    currentVersion: string;
	    latestVersion: string;
	    tagName: string;
	    name: string;
	    publishedAt: string;
	    body: string;
	    htmlUrl: string;
	    assetName: string;
	    assetUrl: string;
	    checksum: string;
	    installScope: string;
	    updateAvailable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.tagName = source["tagName"];
	        this.name = source["name"];
	        this.publishedAt = source["publishedAt"];
	        this.body = source["body"];
	        this.htmlUrl = source["htmlUrl"];
	        this.assetName = source["assetName"];
	        this.assetUrl = source["assetUrl"];
	        this.checksum = source["checksum"];
	        this.installScope = source["installScope"];
	        this.updateAvailable = source["updateAvailable"];
	    }
	}

}

export namespace store {
	
	export class APIKey {
	    id: number;
	    name: string;
	    description: string;
	    key: string;
	    deletedAt?: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new APIKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.key = source["key"];
	        this.deletedAt = source["deletedAt"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class APIKeyInput {
	    id: number;
	    name: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new APIKeyInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	    }
	}
	export class CallLog {
	    id: number;
	    providerId: string;
	    modelId: string;
	    appName: string;
	    protocol: string;
	    startedAt: string;
	    endedAt?: string;
	    statusCode: number;
	    error?: string;
	    durationMs: number;
	    stream: boolean;
	    inputTokens: number;
	    outputTokens: number;
	    cacheCreationInputTokens: number;
	    cacheReadInputTokens: number;
	    tokenEstimated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CallLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.providerId = source["providerId"];
	        this.modelId = source["modelId"];
	        this.appName = source["appName"];
	        this.protocol = source["protocol"];
	        this.startedAt = source["startedAt"];
	        this.endedAt = source["endedAt"];
	        this.statusCode = source["statusCode"];
	        this.error = source["error"];
	        this.durationMs = source["durationMs"];
	        this.stream = source["stream"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheCreationInputTokens = source["cacheCreationInputTokens"];
	        this.cacheReadInputTokens = source["cacheReadInputTokens"];
	        this.tokenEstimated = source["tokenEstimated"];
	    }
	}
	export class CallLogPage {
	    items: CallLog[];
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new CallLogPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], CallLog);
	        this.total = source["total"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Model {
	    id: string;
	    providerId: string;
	    name: string;
	    capabilities: string;
	    contextLength: number;
	    maxTokens: number;
	    enabled: boolean;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Model(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.providerId = source["providerId"];
	        this.name = source["name"];
	        this.capabilities = source["capabilities"];
	        this.contextLength = source["contextLength"];
	        this.maxTokens = source["maxTokens"];
	        this.enabled = source["enabled"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class ModelInput {
	    id: string;
	    providerId: string;
	    name: string;
	    capabilities: string;
	    contextLength: number;
	    maxTokens: number;
	    enabled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.providerId = source["providerId"];
	        this.name = source["name"];
	        this.capabilities = source["capabilities"];
	        this.contextLength = source["contextLength"];
	        this.maxTokens = source["maxTokens"];
	        this.enabled = source["enabled"];
	    }
	}
	export class Provider {
	    id: string;
	    name: string;
	    type: string;
	    baseUrl: string;
	    apiKey?: string;
	    capabilityConfig: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.capabilityConfig = source["capabilityConfig"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class ProviderInput {
	    id: string;
	    name: string;
	    type: string;
	    baseUrl: string;
	    apiKey: string;
	    capabilityConfig: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.capabilityConfig = source["capabilityConfig"];
	    }
	}
	export class ProviderPreset {
	    id: string;
	    name: string;
	    type: string;
	    baseUrl: string;
	    capabilityConfig: string;
	    models: ModelInput[];
	
	    static createFrom(source: any = {}) {
	        return new ProviderPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.capabilityConfig = source["capabilityConfig"];
	        this.models = this.convertValues(source["models"], ModelInput);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TokenStatPoint {
	    date: string;
	    inputTokens: number;
	    outputTokens: number;
	    cacheCreationInputTokens: number;
	    cacheReadInputTokens: number;
	    calls: number;
	    estimatedCalls: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenStatPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.date = source["date"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheCreationInputTokens = source["cacheCreationInputTokens"];
	        this.cacheReadInputTokens = source["cacheReadInputTokens"];
	        this.calls = source["calls"];
	        this.estimatedCalls = source["estimatedCalls"];
	    }
	}
	export class TokenStatRow {
	    name: string;
	    calls: number;
	    inputTokens: number;
	    outputTokens: number;
	    cacheCreationInputTokens: number;
	    cacheReadInputTokens: number;
	    totalTokens: number;
	    share: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenStatRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.calls = source["calls"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheCreationInputTokens = source["cacheCreationInputTokens"];
	        this.cacheReadInputTokens = source["cacheReadInputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.share = source["share"];
	    }
	}
	export class TokenStats {
	    inputTokens: number;
	    outputTokens: number;
	    cacheCreationInputTokens: number;
	    cacheReadInputTokens: number;
	    calls: number;
	    estimatedCalls: number;
	    points: TokenStatPoint[];
	
	    static createFrom(source: any = {}) {
	        return new TokenStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheCreationInputTokens = source["cacheCreationInputTokens"];
	        this.cacheReadInputTokens = source["cacheReadInputTokens"];
	        this.calls = source["calls"];
	        this.estimatedCalls = source["estimatedCalls"];
	        this.points = this.convertValues(source["points"], TokenStatPoint);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TokenStatsFilter {
	    from: string;
	    to: string;
	    providerId: string;
	    modelId: string;
	    appName: string;
	
	    static createFrom(source: any = {}) {
	        return new TokenStatsFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.providerId = source["providerId"];
	        this.modelId = source["modelId"];
	        this.appName = source["appName"];
	    }
	}
	export class TokenTrendPoint {
	    bucket: string;
	    name: string;
	    calls: number;
	    inputTokens: number;
	    outputTokens: number;
	    cacheCreationInputTokens: number;
	    cacheReadInputTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenTrendPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bucket = source["bucket"];
	        this.name = source["name"];
	        this.calls = source["calls"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheCreationInputTokens = source["cacheCreationInputTokens"];
	        this.cacheReadInputTokens = source["cacheReadInputTokens"];
	    }
	}

}
