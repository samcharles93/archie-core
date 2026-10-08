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
 <TabsTrigger value="startup">Startup settings</TabsTrigger>
      </TabsList>
      <TabsContent value="startup">
        <p class="mb-4 text-sm text-fg-subtle">These settings configure process startup. They are read-only here and require a restart.</p>
        <dl class="divide-y divide-border rounded-lg border border-border bg-card">
          <div v-for="(reason,key) in config?.bootstrap" :key="key" class="grid gap-2 px-4 py-3 md:grid-cols-[18rem_1fr]">
            <dt class="font-mono text-xs">{{ key }}</dt><dd class="text-sm text-fg-subtle">{{ reason }}</dd>
          </div>
        </dl>
      </TabsContent>
      <TabsContent value="services"><ServicesCard /></TabsContent>
      <TabsContent value="health"><HealthStatusCard /></TabsContent>
      <TabsContent value="versions" class="space-y-4">
        <VersionsCard />
        <UpdateSettingsCard />
      </TabsContent>
    </Tabs>
  </div>
</template>
