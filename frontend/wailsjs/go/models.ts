export namespace store {
	
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
	    }
	}
	export class TokenStatPoint {
	    date: string;
	    inputTokens: number;
	    outputTokens: number;
	    cacheCreationInputTokens: number;
	    cacheReadInputTokens: number;
	    calls: number;
	
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
	    }
	}
	export class TokenStats {
	    inputTokens: number;
	    outputTokens: number;
	    cacheCreationInputTokens: number;
	    cacheReadInputTokens: number;
	    calls: number;
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
	
	    static createFrom(source: any = {}) {
	        return new TokenStatsFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.providerId = source["providerId"];
	        this.modelId = source["modelId"];
	    }
	}

}

