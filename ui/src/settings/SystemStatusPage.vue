<script setup lang="ts">
import { onMounted, onUnmounted } from "vue";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { config, loadConfig } from "./state";
let timer: ReturnType<typeof setInterval>;
onMounted(() => { void loadConfig(); timer = setInterval(() => void loadConfig(), 15_000); });
onUnmounted(() => clearInterval(timer));
import PageHeader from "@/base/PageHeader.vue";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import HealthStatusCard from "./HealthStatusCard.vue";
import ServicesCard from "./ServicesCard.vue";
import UpdateSettingsCard from "./UpdateSettingsCard.vue";
import VersionsCard from "./VersionsCard.vue";
</script>

<template>
  <div>
    <PageHeader title="Status" />
    <Alert v-if="config?.reload?.last_error" variant="destructive" class="mb-4">
      <AlertTitle>Configuration reload failed</AlertTitle>
      <AlertDescription>{{ config.reload.last_error }}</AlertDescription>
    </Alert>

    <Tabs default-value="services" class="space-y-4">
      <TabsList>
        <TabsTrigger value="services">Services</TabsTrigger>
        <TabsTrigger value="health">Health</TabsTrigger>
        <TabsTrigger value="versions">Versions</TabsTrigger>
      </TabsList>
      <TabsContent value="services"><ServicesCard /></TabsContent>
      <TabsContent value="health"><HealthStatusCard /></TabsContent>
      <TabsContent value="versions" class="space-y-4">
        <VersionsCard />
        <UpdateSettingsCard />
      </TabsContent>
    </Tabs>
  </div>
</template>
