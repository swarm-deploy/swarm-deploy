import { isNavigationFailure, NavigationFailureType, type LocationQueryRaw, type Router } from "vue-router";
import { useAssistantStore } from "../stores/assistant";

// The drawer selects a fresh chat when the route changes to "new".
export async function openNewAssistantChat(router: Router, query: LocationQueryRaw = router.currentRoute.value.query) {
  const route = router.currentRoute.value;
  if (route.query.assistant === "new") useAssistantStore().newChat();
  const failure = await router.push({ query: { ...query, assistant: "new" }, hash: route.hash });
  return !failure || isNavigationFailure(failure, NavigationFailureType.duplicated);
}
