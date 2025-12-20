import type { SearchRequest, SearchResponse, StatsResponse, HealthResponse } from './types';

// Use proxy in development, direct URL in production
const API_BASE_URL = import.meta.env.VITE_API_URL || '/api';

export class ApiClient {
	/**
	 * Search documentation using the RAG API
	 */
	async search(query: string, limit: number = 3): Promise<SearchResponse> {
		const request: SearchRequest = { query, limit };

		const response = await fetch(`${API_BASE_URL}/search`, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/json'
			},
			body: JSON.stringify(request)
		});

		if (!response.ok) {
			throw new Error(`Search failed: ${response.statusText}`);
		}

		return response.json();
	}

	/**
	 * Get database statistics
	 */
	async getStats(): Promise<StatsResponse> {
		const response = await fetch(`${API_BASE_URL}/stats`);

		if (!response.ok) {
			throw new Error(`Failed to get stats: ${response.statusText}`);
		}

		return response.json();
	}

	/**
	 * Check API health
	 */
	async checkHealth(): Promise<HealthResponse> {
		const response = await fetch(`${API_BASE_URL}/health`);

		if (!response.ok) {
			throw new Error(`Health check failed: ${response.statusText}`);
		}

		return response.json();
	}
}

export const apiClient = new ApiClient();
