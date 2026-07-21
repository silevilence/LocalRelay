export namespace store {
	
	export class Model {
	    id: string;
	    providerId: string;
	    name: string;
	    capabilities: string;
	    contextLength: number;
	    maxTokens: number;
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

}

