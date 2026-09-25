package projections

func postReactionCountSQL(postIDExpr string) string {
	return "COALESCE((SELECT SUM(count_value) FROM post_reaction_count_shards WHERE post_id=" + postIDExpr + "), (SELECT COUNT(*) FROM post_reactions WHERE post_id=" + postIDExpr + "), 0)"
}

func postReactionAggregateJoinSQL(alias, postIDExpr string) string {
	return "LEFT JOIN (SELECT post_id, SUM(count_value) AS reaction_count FROM post_reaction_count_shards GROUP BY post_id) " + alias + " ON " + alias + ".post_id=" + postIDExpr
}

func postReactionAggregateValueSQL(alias, postIDExpr string) string {
	return "COALESCE(" + alias + ".reaction_count, (SELECT COUNT(*) FROM post_reactions WHERE post_id=" + postIDExpr + "), 0)"
}

func postReactionAggregateSumSQL(alias, postIDExpr string) string {
	return "COALESCE(SUM(" + postReactionAggregateValueSQL(alias, postIDExpr) + "), 0)"
}
