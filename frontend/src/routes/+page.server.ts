import type { PageServerLoad } from './$types';
import type { StatsResponse } from '$lib/types';

const API_URL = process.env.API_URL || 'http://localhost:8080';

export const load: PageServerLoad = async ({ fetch }) => {
	try {
		const response = await fetch(`${API_URL}/stats`);
		if (!response.ok) {
			throw new Error(`Failed to fetch stats: ${response.statusText}`);
		}
		const stats: StatsResponse = await response.json();
		return { stats };
	} catch (error) {
		console.error('Failed to load stats on server:', error);
		return { stats: null };
	}
};
